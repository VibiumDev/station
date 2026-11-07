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
# binary from the npm package for this platform, with the package's
# own install step skipped so no browser is downloaded.
FROM node:22-alpine AS vibium
ARG TARGETARCH
ARG VIBIUM_VERSION=26.8.21
RUN npm install -g --ignore-scripts vibium@${VIBIUM_VERSION} \
 && ARCH=$([ "$TARGETARCH" = "arm64" ] && echo arm64 || echo x64) \
 && install -m 755 /usr/local/lib/node_modules/vibium/node_modules/@vibium/linux-$ARCH/bin/vibium /out-vibium

FROM alpine:3.22
RUN apk add --no-cache ca-certificates tzdata
COPY --from=build /out/station /out/station-probe /usr/local/bin/
COPY --from=vibium /out-vibium /usr/local/bin/vibium
ENV STATION_DIR=/data
VOLUME /data
EXPOSE 8443 3478/udp
CMD ["station"]
