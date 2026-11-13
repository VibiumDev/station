#!/usr/bin/env python3
"""Speech to text over a WebSocket, with sherpa-onnx's streaming recognizer.

The station starts this and keeps it running, so a jam's listen finds the
model loaded. It speaks the protocol of sherpa-onnx's own WebSocket
servers: a client sends 32-bit float samples as binary messages and the
text "Done" at the end, and gets JSON results and then the text "Done!".
Their servers listen on every interface; this one listens on 127.0.0.1
unless told otherwise, and turns a browser's page away.

Needs sherpa-onnx, and websockets 13 or newer: pip install sherpa-onnx websockets
"""

import argparse
import array
import asyncio
import http
import json
import os
import sys
from concurrent.futures import ThreadPoolExecutor

import sherpa_onnx
from websockets.asyncio.server import serve
from websockets.exceptions import ConnectionClosed

# Silence is fed after the audio so the last words are decoded: a model
# works in chunks and gives a word some time after hearing it. Enough is
# two more chunks, which is 0.6 s for one model and 2.2 s for another, so
# the server counts chunks, within these limits in seconds.
TAIL_CHUNKS = 2
TAIL_MIN, TAIL_MAX, TAIL_STEP = 0.8, 8.0, 0.2


def parse_args():
    p = argparse.ArgumentParser(description=__doc__.split("\n")[0])
    p.add_argument("--model", required=True, help="a streaming transducer model's folder: tokens.txt and the encoder, decoder and joiner .onnx files")
    p.add_argument("--host", default="127.0.0.1")
    p.add_argument("--port", type=int, default=6006)
    p.add_argument("--threads", type=int, default=1, help="threads for the neural network")
    p.add_argument("--sample-rate", type=int, default=16000, help="the rate clients send")
    p.add_argument("--max-seconds", type=int, default=1800, help="the most audio one connection may send")
    p.add_argument("--max-clients", type=int, default=8)
    return p.parse_args()


def model_files(folder):
    """The model's four files. Of a part's files, the int8 one is taken: it is
    the faster on a CPU."""
    files = {"tokens": os.path.join(folder, "tokens.txt")}
    if not os.path.isfile(files["tokens"]):
        sys.exit(f"{folder} has no tokens.txt")
    names = sorted(os.listdir(folder))
    for part in ("encoder", "decoder", "joiner"):
        found = [n for n in names if n.startswith(part) and n.endswith(".onnx")]
        if not found:
            sys.exit(f"{folder} has no {part}*.onnx")
        int8 = [n for n in found if n.endswith(".int8.onnx")]
        files[part] = os.path.join(folder, (int8 or found)[0])
    return files


def main():
    args = parse_args()
    name = os.path.basename(os.path.normpath(args.model))
    recognizer = sherpa_onnx.OnlineRecognizer.from_transducer(
        **model_files(args.model),
        num_threads=args.threads,
        sample_rate=16000,
        feature_dim=80,
        enable_endpoint_detection=True,
        rule3_min_utterance_length=3600,  # a new segment after a pause, never in the middle of speech
        decoding_method="greedy_search",
    )
    pool = ThreadPoolExecutor(max_workers=1)
    clients = asyncio.Semaphore(args.max_clients)
    info = json.dumps({"model": name, "sample_rate": args.sample_rate})

    def feed(stream, samples):
        stream.accept_waveform(args.sample_rate, samples)
        while recognizer.is_ready(stream):
            recognizer.decode_stream(stream)

    def finish(stream):
        silence = array.array("f", bytes(4 * int(TAIL_STEP * args.sample_rate)))
        chunks, fed = 0, 0.0
        while (chunks < TAIL_CHUNKS or fed < TAIL_MIN) and fed < TAIL_MAX:
            stream.accept_waveform(args.sample_rate, silence)
            fed += TAIL_STEP
            while recognizer.is_ready(stream):
                recognizer.decode_stream(stream)
                chunks += 1
        stream.input_finished()
        while recognizer.is_ready(stream):
            recognizer.decode_stream(stream)

    def process_request(connection, request):
        # A page in a browser always says where it is from; a program does not.
        if "Origin" in request.headers:
            return connection.respond(http.HTTPStatus.FORBIDDEN, "not for a browser's page\n")
        if request.path == "/info":
            response = connection.respond(http.HTTPStatus.OK, info)
            del response.headers["Content-Type"]
            response.headers["Content-Type"] = "application/json"
            return response
        return None

    async def client(ws):
        if clients.locked():
            await ws.close(1013, "too many clients; try again")
            return
        async with clients:
            loop = asyncio.get_running_loop()
            stream = recognizer.create_stream()
            sent, total = None, 0
            limit = args.max_seconds * args.sample_rate

            async def result(final, eof=False):
                # The recognizer's own result: the text so far in this
                # segment, the segment's number and where it starts. A
                # final one also carries the tokens and their times.
                nonlocal sent
                r = json.loads(recognizer.get_result_as_json_string(stream))
                if not final and (r["text"], r["segment"]) == sent:
                    return
                sent = (r["text"], r["segment"])
                out = {"text": r["text"], "segment": r["segment"], "start_time": r["start_time"], "is_final": final, "is_eof": eof}
                if final:
                    out["tokens"], out["timestamps"] = r["tokens"], r["timestamps"]
                await ws.send(json.dumps(out))

            try:
                async for message in ws:
                    if isinstance(message, str):
                        if message.strip() == "Done":
                            break
                        continue
                    samples = array.array("f")
                    samples.frombytes(message[: len(message) // 4 * 4])
                    if sys.byteorder == "big":
                        samples.byteswap()
                    total += len(samples)
                    if total > limit:
                        await ws.close(1009, "too much audio for one connection")
                        return
                    await loop.run_in_executor(pool, feed, stream, samples)
                    await result(False)
                    if recognizer.is_endpoint(stream):
                        await result(True)
                        recognizer.reset(stream)
                        sent = None
                else:
                    return  # the client left without saying Done
                await loop.run_in_executor(pool, finish, stream)
                await result(True, eof=True)
                await ws.send("Done!")
            except ConnectionClosed:
                pass

    async def run():
        async with serve(client, args.host, args.port, process_request=process_request, max_size=16 << 20):
            print(f"sherpa-onnx: {name} ready at ws://{args.host}:{args.port}", flush=True)
            await asyncio.get_running_loop().create_future()

    try:
        asyncio.run(run())
    except KeyboardInterrupt:
        pass
    return 0


if __name__ == "__main__":
    sys.exit(main())
