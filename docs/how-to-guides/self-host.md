# Self-host a station

This guide runs a Vibium Station on a Linux machine of your own: a spare
Raspberry Pi, a small VPS, or a box under the desk. The station becomes
a systemd service under a user of its own, sandboxed and restarted by
itself, and keeps everything it owns in one directory. The recommended
mode is tailnet mode, where the station joins your Tailscale network as
a node and nothing is exposed to the Internet: a station on the same Pi
as Roadie makes quick work of your own testing and a small team's. LAN
mode, public mode and a TLS proxy are the alternatives. To run one on
Fly.io instead, see [deploy-to-fly.md](deploy-to-fly.md).

You need a host with systemd and `sudo`, and for tailnet mode a
Tailscale account with MagicDNS and HTTPS certificates enabled on the
tailnet (the DNS page of the admin console).

## 1. Get the binary

No release is published yet, so build the binaries from a clone of the
repository. That needs Go 1.27.4 or newer:

```bash
git clone https://github.com/VibiumDev/station
cd station
make build
sudo install -m 755 station station-linecheck /usr/local/bin/
station version
```

Run the commands of the next steps from the clone, since they use the
files under `deploy/`. A release will carry a tarball per architecture
with the same two binaries, the systemd unit and the sample `fly.toml`;
`make dist` builds them. The Docker image is covered [below](#docker).

Jam sessions, the prompt on the station's front page, also need the
Vibium binary. [Set Up Jams](set-up-jams.md#1-vibium) has three ways to
get it: a tagged release, a nightly, or your own build.

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

The owner's login is `owner`. The password needs at least 12
characters and may not contain the login. A change applies at once,
with no restart. Hosts that keep secrets in environment
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

A new node has to log in once. An auth key made in the admin console
and set as `TS_AUTHKEY` in `/etc/default/station` lets it join on its
own, which suits a service best. Without a key the station prints a
login link to its journal (`journalctl -u station -f`) at its first
start, for you to open in a browser, and is not up until you have.
Systemd allows a start 90 seconds by default. Past that it stops the
station and starts it again, and goes on doing so until the login is
done.
The TURN relay stays off in this mode, because a browser and a rig on
the same tailnet reach each other directly, and when WebRTC fails the
viewer falls back to the MJPEG stream relayed through the station.

An identity on `--allow` becomes an admin the first time it opens the
station, as a user of its own under Users on the Settings page's Access tab. To take that
access away, remove or lock the user there and take the login out of
`--allow`. The list is saved in `config.json`: a changed `--allow`
replaces it at the next start, but leaving the flag out keeps the saved
list, so delete `allow` from `config.json` then and restart.

### LAN mode

```bash
STATION_ARGS=""
```

With no mode flag the station serves
`https://<this machine's address>:8443` to the local network, with a
certificate it makes itself, so a browser warns about it. Rigs and
browsers have to be on the same network. People sign in with a login
and a password, TURN stays off, and the station announces itself over
mDNS, so a rig on the same network enrolls without being given the
address.

- The address is the machine's first private IPv4 address.
  `--public-url https://192.168.1.20:8443` names another.
- A rig remembers the address it enrolled with, so give the machine a
  fixed address.
- Open TCP 8443 when the host has a firewall. `--listen` changes the
  port.

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
counts by. Two more things are the proxy's to get right:

- It passes the `Host` header on unchanged (nginx:
  `proxy_set_header Host $host;`). The station refuses a browser's
  WebSocket when the page's origin names another host than the request
  does, and the viewer would not connect.
- It sets `X-Forwarded-For` itself rather than adding to what a client
  sent (nginx: `proxy_set_header X-Forwarded-For $remote_addr;`), and
  drops any `Fly-Client-IP` header from a client. The lockout takes the
  address from `Fly-Client-IP` when there is one, and otherwise from the
  first entry of `X-Forwarded-For`.

## 5. Start it

```bash
sudo systemctl enable --now station
systemctl status station
journalctl -u station -f
```

The journal shows the mode and the address, the station's key as an
`npub`, and in tailnet mode the login link. A line saying `no password
yet` means step 3 was skipped. In tailnet mode without an auth key the
first command waits for the login, so follow the journal from a second
terminal.

## 6. First steps

[First Steps on a New Station](first-steps.md) takes it from here: sign in
and set up your authenticator, enroll a rig, and check the whole path with
`station-linecheck`.

## Docker

No image is published yet, so build one from the clone. A release will
publish it as `ghcr.io/vibiumdev/station`.

```bash
docker build -t vibium-station .
```

The image holds the same binaries and a Vibium nightly, with the data
directory at `/data`. [Set Up Jams](set-up-jams.md#1-vibium) covers
another nightly or your own build. A public station:

```bash
docker run -d --name station --restart unless-stopped \
  -v station_data:/data \
  -p 80:80 -p 443:443 -p 3478:3478/udp -p 49152-49215:49152-49215/udp \
  vibium-station \
  station --public station.example.com --turn-ports 49152-49215
```

A tailnet-mode one publishes no ports at all, since tsnet dials out:

```bash
docker run -d --name station --restart unless-stopped \
  -v station_data:/data -e TS_AUTHKEY=tskey-auth-... \
  vibium-station \
  station --tailnet --allow you@example.com
```

The commands run inside the container, where `STATION_DIR` is already
set: `docker exec -it station station passwd`, and the same for `token
create` and `enroll`.

## Afterwards

**Updating.** Back up the data directory first, as below. Then build
in the clone, install the binaries over the old ones and restart:

```bash
git pull
make build
sudo install -m 755 station station-linecheck /usr/local/bin/
sudo systemctl restart station
```

Rigs reconnect by themselves. People sign in again, because sessions
are kept in memory. When `deploy/systemd/station.service` changed,
install it again as in step 2 and run `sudo systemctl daemon-reload`
before the restart. With Docker, build the image again and recreate the
container with the same volume.

**Backups.** Everything is in `/var/lib/station`. Stop the service,
copy the directory and start it again. What the copy holds:

- `config.json`: the name, the allow list and the two Jams switches.
- `jam-notes.md`: the station's jam notes, when an admin wrote any.
  It is plain text and can be edited by hand.
- `station.key`: the station's identity. Every rig pins it at
  enrollment, so losing it means enrolling every rig again.
- `station.db`, with its `-wal` and `-shm` companions: users with their
  password hashes and authenticator secrets, teams, rigs, devices,
  hashed tokens, jams and their transcripts, the Record Store's index,
  the signed events and the audit table.
- `cert.pem` and `key.pem`: the LAN-mode certificate. A rig pins it at
  enrollment, so losing it means enrolling every LAN rig again.
- `acme/`: certificates, fetched again if lost.
- `tsnet/`: the tailnet node's identity, which a fresh copy would have
  to log in for again.
- `blobs/`: the Record Store's files, with each recording unpacked
  beside its zip under `blobs/unpacked/`.
- `ai/`: the AI models and their API keys, which make the copy a
  secret.
- `jams/`: the screenshots in the jams' transcripts.
- `vibium/`: Vibium's working files, which a copy can leave out.

**The Record Store.** Recordings, screenshots and logs are files in
`/var/lib/station/blobs`. The defaults:

- The store keeps 2 GB in all. Past that the oldest files go.
- A file is at most 256 MB.
- A file is kept for 30 days.
- A recording is also kept unpacked, so the store takes about twice its
  limit on disk.

`--records-max-total`, `--records-max-file` and `--records-max-age` in
`STATION_ARGS` change the limits. The unit lets the station write only
under `/var/lib/station`. A `--records-dir` elsewhere needs a directory
the `station` user owns and a drop-in for the unit
(`sudo systemctl edit station`) with `ReadWritePaths=` naming it. The
screenshots under `jams/` are outside these limits and are not removed.

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
- `station-linecheck` fails at the hold: the token is read-only, or someone
  else holds the device.
- The sign-in page refuses a right password: five misses from one
  address lock that address out for up to 15 minutes, and each miss
  after the second makes that login wait longer.
- The authenticator's codes are refused: the station's clock or the
  phone's is off. `timedatectl` says whether the station's is
  synchronized.
- A forgotten password or a lost authenticator: see
  [If you're locked out](first-steps.md#if-youre-locked-out).
