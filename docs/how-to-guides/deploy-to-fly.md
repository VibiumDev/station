# Deploy a station to Fly.io

This guide puts a public Vibium Station on [Fly.io](https://fly.io). It
runs on one machine. TLS ends at Fly's edge. The station's data lives on
a volume, and the TURN relay listens on the app's own IPv4 address. The
guide assumes you have a Fly account, that the `fly` command is signed
in, and that you have a clone of this repository.

The result is a station at `https://NAME.fly.dev` that rigs anywhere can
enroll with and that relays video through TURN when a direct path fails.

## 1. Create the app, its address and its volume

Pick a name and a [region](https://fly.io/docs/reference/regions/) near
your rigs and the people using them:

```bash
fly apps create NAME
fly ips allocate-v4 -a NAME
fly volumes create station_data --size 1 -r REGION -a NAME
```

The dedicated IPv4 is for TURN, because Fly carries UDP only on a
dedicated address. Fly bills it separately. Note the address that
`fly ips allocate-v4` prints, because the configuration below needs it.
The volume holds the station's configuration, its key and its database,
so they survive deploys and restarts. One gigabyte is plenty.

## 2. Set the owner's password

```bash
fly secrets set STATION_PASSWORD='a password of at least 8 characters' -a NAME
```

The station reads it at every start and saves the hash in its
configuration on the volume. To change the password later, set the
secret again and redeploy.

## 3. Write the configuration

Copy [`deploy/fly/fly.toml`](../../deploy/fly/fly.toml) and change four
things in it:

- `app`: your NAME.
- `primary_region`: your REGION.
- In `[processes]`: `--public-url https://NAME.fly.dev`, and `--name`,
  which is the station's name as its pages show it.
- In `[processes]`, `--turn-public-ip`: the IPv4 address from step 1.

Leave the rest. It runs the station on plain HTTP on port 8080 behind
Fly's TLS. It binds TURN to `fly-global-services:3478`, with relay ports
49152 to 49215 on the same address. It keeps one machine always running
so the rigs' tunnels stay up, and it mounts the volume at `/data`.

## 4. Deploy

From the repository root:

```bash
fly deploy -c deploy/fly/fly.toml --dockerfile Dockerfile --remote-only --ha=false -a NAME
```

`--ha=false` keeps the app to one machine. The volume requires that, and
so does the station's in-memory queue of holds. The first deploy builds
the image on Fly and takes a few minutes. `fly logs -a NAME` shows the
station starting, and the first time it prints `Password set from
STATION_PASSWORD`.

## 5. Check it

Open `https://NAME.fly.dev` and sign in with the password. Settings
shows the station's name, its key and the TURN line. The TURN line
should show the public address from step 1.

Then enroll a rig. Make an enrollment code on Settings. On the rig, run
`roadie enroll https://NAME.fly.dev --code ABCD-1234-7F3A`. The rig
appears on the front page within seconds.

To prove the relay path end to end, make an API token on Settings and
run the probe from your machine. Relay-only media is its default:

```bash
go run ./cmd/station-probe -station https://NAME.fly.dev -token stk_... -device RIG/DEVICE
```

The probe takes the device and streams over WebRTC through TURN. It taps
the screen, checks that the picture changed, and releases the device. A
non-zero exit names what failed.

## Afterwards

- **Updating**: run the deploy command again. The volume keeps the
  configuration, the key and the database. Live holds and sessions end
  with the restart. The rigs reconnect on their own.
- **Logs**: `fly logs -a NAME`.
- **Tokens and codes without the browser**:
  `fly ssh console -a NAME -C "station token create laptop"` and
  `fly ssh console -a NAME -C "station enroll"`.
- **More relay capacity**: TURN uses one port per allocation, so the
  64-port range serves 64 media sessions at once. Widen `--turn-ports`
  and the matching `[[services]]` port range together.
- **Login lockouts** count by the client's address as Fly reports it in
  `Fly-Client-IP`, since every connection reaches the station from Fly's
  proxy.
