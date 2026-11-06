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

FROM alpine:3.22
RUN apk add --no-cache ca-certificates tzdata
COPY --from=build /out/station /out/station-probe /usr/local/bin/
ENV STATION_DIR=/data
VOLUME /data
EXPOSE 8443 3478/udp
CMD ["station"]
