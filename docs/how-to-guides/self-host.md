# Self-host a station

This guide runs a Vibium Station on a Linux machine of your own: a spare
Raspberry Pi, a small VPS, or a box under the desk. The station becomes
a systemd service under a user of its own, sandboxed and restarted by
itself, and keeps everything it owns in one directory. The recommended
mode is tailnet mode, where the station joins your Tailscale network as
a node and nothing is exposed to the Internet, with public mode and a
TLS proxy as the alternatives. To run one on Fly.io instead, see
[deploy-to-fly.md](deploy-to-fly.md).

You need a host with systemd and `sudo`, and for tailnet mode a
Tailscale account with MagicDNS and HTTPS certificates enabled on the
tailnet (the DNS page of the admin console).

## 1. Get the binary

Each [release](https://github.com/VibiumDev/station/releases) carries a
tarball per architecture with `station`, `station-probe`, the systemd
unit, the sample `fly.toml` and a checksum file. Pick `arm64` for a Pi or
an ARM server and `amd64` for an x86 machine (`uname -m` says `aarch64`
or `x86_64`), then:

```bash
VERSION=v0.1.0
ARCH=arm64
curl -fLO https://github.com/VibiumDev/station/releases/download/$VERSION/station_${VERSION}_linux_$ARCH.tar.gz
curl -fLO https://github.com/VibiumDev/station/releases/download/$VERSION/sha256sums.txt
sha256sum -c --ignore-missing sha256sums.txt
tar xzf station_${VERSION}_linux_$ARCH.tar.gz
cd station_${VERSION}_linux_$ARCH
sudo install -m 755 station station-probe /usr/local/bin/
station version
```

Building from source needs Go 1.27 or newer and a clone of the
repository, where `make build` produces the same two binaries and
`make dist` the same tarballs. The Docker image is covered
[below](#docker).

Jams, the prompt on the station's front page, drive devices through
`vibium mcp`, so the station also needs the Vibium binary, on its
`PATH` or named with `--vibium` (or `STATION_VIBIUM` in
`/etc/default/station`). The npm package carries a static binary for
each platform; without a Node install, pick the package for the
machine out of the registry:

```bash
ARCH=arm64   # or x64
curl -fL "https://registry.npmjs.org/@vibium/linux-$ARCH/-/linux-$ARCH-26.8.21.tgz" | tar xz
sudo install -m 755 package/bin/vibium /usr/local/bin/
vibium --version
```

Vibium needs no browser for this: a jam's session is the station's own
device, reached through `/session`.

## 2. The user and the service

The service runs as a system user named `station` whose home is the
data directory. The unit creates `/var/lib/station` at the first start,
and making it now lets you set the password before that:

```bash
sudo useradd --system --home-dir /var/lib/station --shell /usr/sbin/nologin station
sudo install -d -o station -g station -m 700 /var/lib/station
sudo install -m 644 deploy/systemd/station.service /etc/systemd/system/
sudo install -m 600 deploy/systemd/station.default /etc/default/station
```

`/etc/default/station` holds the station's arguments and any secrets
you choose to keep there, which is why it is readable by root alone.
The unit is the hardened one from the repository. It gives the station
a read-only view of the system with no home directories and no device
nodes, restarts it within two seconds whenever the process ends, and
restarts it as well when the watchdog finds it has stopped answering.

## 3. Set the owner's password

Every command that touches the station's files runs as its user and
names the data directory:

```bash
sudo -u station station passwd --data-dir /var/lib/station
```

The password needs at least 8 characters. The station reads the hash
when it starts, so set the password before the first start, or restart
the service after changing it. Hosts that keep secrets in environment
variables can put `STATION_PASSWORD` in `/etc/default/station` instead,
which applies at every start.

## 4. Choose the mode

Edit `STATION_ARGS` in `/etc/default/station`. The file ships set up for
tailnet mode.

### Tailnet mode

```bash
STATION_ARGS="--tailnet --hostname station --allow you@example.com"
```

The station joins the tailnet as a node named by `--hostname` and
serves `https://station.<tailnet>.ts.net` with a certificate Tailscale
issues, which is why MagicDNS and HTTPS have to be enabled for the
tailnet. People whose logins or tags are in `--allow` sign in by
identity without typing anything; everyone else on the tailnet gets the
password page. Only tailnet members can reach the station at all, so
every rig and every browser that uses it has to be on the tailnet too.

A new node has to log in once. At its first start the station prints
a login link to its journal (`journalctl -u station -f`) for you to
open in a browser. An auth key made in the admin console and set as
`TS_AUTHKEY` in `/etc/default/station` lets it join on its own instead.
The TURN relay stays off in this mode, because a browser and a rig on
the same tailnet reach each other directly, and when WebRTC fails the
viewer falls back to the MJPEG stream relayed through the station.

### Public mode

```bash
STATION_ARGS="--public station.example.com --turn-ports 49152-49215"
```

Point the name's A or AAAA record at the host and open TCP 80 and 443,
UDP 3478 and the relay range from `--turn-ports` (narrowed here to 64
ports; the default is the whole ephemeral range). The station fetches a
Let's Encrypt certificate for the name into `/var/lib/station/acme` and
renews it itself, answering the challenge on port 80, and the unit
grants it the one capability that binding those ports needs. Behind a
NAT, `--turn-public-ip` names the public address TURN is reached at.

### Behind your own TLS proxy

```bash
STATION_ARGS="--http :8080 --public-url https://station.example.com --turn-public-ip 203.0.113.9"
```

The station then speaks plain HTTP on `:8080` and the proxy terminates
TLS at the public name. The proxy has to pass WebSockets through (the
rigs' tunnel, signaling and the BiDi relay are all WebSockets) and to
send the client's address in `X-Forwarded-For`, which the login lockout
counts by.

## 5. Start it

```bash
sudo systemctl enable --now station
systemctl status station
journalctl -u station -f
```

The journal shows the mode and the address, the station's key as an
`npub`, and in tailnet mode the login link. A line saying `no password
yet` means step 3 was skipped.

## 6. Tokens and the first rig

An API token lets an agent or `station-probe` in, and the secret is
shown once:

```bash
sudo -u station station token create laptop --data-dir /var/lib/station
```

A rig enrolls with a one-time code, from the Settings page or the
command line:

```bash
sudo -u station station enroll --data-dir /var/lib/station
```

On the rig, `roadie enroll https://station.<tailnet>.ts.net --code
ABCD-1234-7F3A` and a restart of `roadie` bring the device onto the
station's front page; Roadie's [How to Run
Roadie](https://github.com/VibiumDev/roadie/blob/main/docs/how-to/run-roadie.md)
has the details under "Join a station". The same page covers reaching
Roadie directly on the tailnet with `--auth tailscale` and HTTPS through
`tailscale serve` or `tailscale cert`, which works beside a station: the
rig keeps its tunnel to the station for everyone else while people on
the tailnet open its own `/view`.

Then check the whole path from another machine, through TURN only:

```bash
station-probe -station https://station.<tailnet>.ts.net -token stk_... -device RIG/DEVICE
```

It takes a hold, connects relay-only, watches the picture for ten
seconds, taps, and exits non-zero when anything fails. Add `-insecure`
for a LAN-mode station's self-signed certificate.

## Docker

The image at `ghcr.io/vibiumdev/station` holds the same binaries, with
the data directory at `/data`. A public station:

```bash
docker run -d --name station --restart unless-stopped \
  -v station_data:/data \
  -p 80:80 -p 443:443 -p 3478:3478/udp -p 49152-49215:49152-49215/udp \
  ghcr.io/vibiumdev/station:latest \
  station --public station.example.com --turn-ports 49152-49215
```

A tailnet-mode one publishes no ports at all, since tsnet dials out:

```bash
docker run -d --name station --restart unless-stopped \
  -v station_data:/data -e TS_AUTHKEY=tskey-auth-... \
  ghcr.io/vibiumdev/station:latest \
  station --tailnet --allow you@example.com
```

The commands run inside the container, where `STATION_DIR` is already
set: `docker exec -it station station passwd`, and the same for `token
create` and `enroll`.

## Afterwards

**Updating.** Install the new binary over the old one and restart:
`sudo install -m 755 station /usr/local/bin/ && sudo systemctl restart
station`. Rigs reconnect by themselves. With Docker, pull the new image
and recreate the container with the same volume.

**Backups.** Everything is in `/var/lib/station`. Stop the service,
copy the directory and start it again. What the copy holds:

- `config.json`: the name, the password hash and the allow list.
- `jam-notes.md`: the station's jam notes, when an admin wrote any.
  It is plain text and can be edited by hand.
- `station.key`: the station's identity. Every rig pins it at
  enrollment, so losing it means enrolling every rig again.
- `station.db`, with its `-wal` and `-shm` companions: rigs, devices,
  hashed tokens, the signed events and the audit table.
- `acme/`: certificates, fetched again if lost.
- `tsnet/`: the tailnet node's identity, which a fresh copy would have
  to log in for again.

**Logs.** `journalctl -u station` holds the station's output, `-f`
follows it and `-b` shows the current boot. A `Watchdog timeout` line
means the handler stopped answering for thirty seconds and systemd
restarted the station.

**When something is off:**

- The login link never appears in tailnet mode: the node was already
  approved on an earlier run (the state is in `tsnet/`), or
  `TS_AUTHKEY` is set and has expired.
- `https://station.<tailnet>.ts.net` does not resolve or has no
  certificate: enable MagicDNS and HTTPS certificates for the tailnet.
- Public mode fails to get a certificate: port 80 is not reachable from
  the Internet, or the name does not point at this host yet.
- The viewer connects but shows no picture across networks: UDP 3478 or
  the relay range is closed, or `--turn-public-ip` is wrong.
- A rig shows offline: its own journal (`journalctl -u roadie`) says why
  the tunnel is not up.
- `station-probe` fails at the hold: the token is read-only, or someone
  else holds the device.
