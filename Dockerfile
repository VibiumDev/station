# Vibium Station, one static binary in a small image. The data directory
# (config, key, database, certificates) is /data: mount a volume there.
FROM golang:1.27-alpine AS build
ARG VERSION=dev
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /out/station ./cmd/station \
 && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /out/station-probe ./cmd/station-probe

# Vibium, whose MCP server a jam drives devices through: the static
# binary of a nightly build, checked against that build's own list of
# checksums. The last release, 26.8.21, cannot tap or swipe on a phone or
# send a jam's record, so a nightly stands in until a release can.
FROM alpine:3.22 AS vibium
ARG TARGETARCH
ARG VIBIUM_NIGHTLY=nightly-2026.11.10-dev.20261110220518-01f5372
RUN apk add --no-cache curl \
 && case "${TARGETARCH:-$(uname -m)}" in arm64|aarch64) ARCH=arm64 ;; *) ARCH=amd64 ;; esac \
 && URL=https://github.com/VibiumDev/vibium/releases/download/${VIBIUM_NIGHTLY} \
 && curl -fsSLo /out-vibium $URL/vibium-linux-$ARCH \
 && curl -fsSLo /tmp/SHA256SUMS $URL/SHA256SUMS \
 && want=$(grep " release/bin/vibium-linux-$ARCH\$" /tmp/SHA256SUMS | cut -d' ' -f1) \
 && [ -n "$want" ] && [ "$want" = "$(sha256sum /out-vibium | cut -d' ' -f1)" ] \
 && chmod 755 /out-vibium

FROM alpine:3.22
RUN apk add --no-cache ca-certificates tzdata
COPY --from=build /out/station /out/station-probe /usr/local/bin/
COPY --from=vibium /out-vibium /usr/local/bin/vibium
ENV STATION_DIR=/data
VOLUME /data
EXPOSE 8443 3478/udp
CMD ["station"]
