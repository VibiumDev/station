# Self-host a station

This guide runs a Vibium Station on a Linux machine of your own: a spare
Raspberry Pi, a small VPS, or a box under the desk. The station becomes
a systemd service under a user of its own, sandboxed and restarted by
itself, and keeps everything it owns in one directory. The recommended
mode is tailnet mode, where the station joins your Tailscale network as
a node and nothing is exposed to the Internet, with LAN mode, public
mode and a TLS proxy as the alternatives. To run one on Fly.io instead, see
[deploy-to-fly.md](deploy-to-fly.md).

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
sudo install -m 755 station station-probe /usr/local/bin/
station version
```

Run the commands of the next steps from the clone, since they use the
files under `deploy/`. A release will carry a tarball per architecture
with the same two binaries, the systemd unit and the sample `fly.toml`;
`make dist` builds them. The Docker image is covered [below](#docker).

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

That is Vibium 26.8.21, the newest npm release. With it a jam on a
phone can take screenshots, press keys and wait. It cannot tap, swipe,
long press or type text, it cannot say or listen, and its own record is
not sent to the station.

- Tap, swipe and long press came with the Vibium nightly of 2026-10-22.
- Typing text and sending the record are both in from the nightly of
  2026-11-09.
- Say and listen need a build of Vibium's `main`.

The nightlies are among Vibium's
[releases](https://github.com/VibiumDev/vibium/releases), each with a
`vibium-linux-arm64` and a `vibium-linux-amd64` binary. A nightly
binary or a build of `main` goes to `/usr/local/bin/vibium`, in place
of the one above. The station looks for Vibium when it starts, so
restart the station after installing or replacing it.

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
station, as a user of its own under Users on Settings. To take that
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

## 6. Sign in

Open the station's address in a browser:

- Tailnet mode: `https://station.<tailnet>.ts.net`. A login on
  `--allow` is signed in at once, as an admin. Anyone else gets the
  sign-in page.
- LAN mode: `https://<this machine's address>:8443`. The browser warns
  about the station's own certificate.
- Public mode: `https://station.example.com`.

On the sign-in page the login is `owner` and the password is the one
from step 3. The first password sign-in shows a QR code. Scan it with
an authenticator app and type the six-digit code the app shows. The
station then lists ten backup codes, once. Keep them: each signs you in
one time when the authenticator is out of reach. Every later password
sign-in asks for a code from the app. The codes follow the clock, so
the station's clock has to be right.

## 7. Tokens and the first rig

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

On the rig, `roadie enroll` with the code and a restart of `roadie`
bring the device onto the station's Devices page. On a Pi where Roadie
runs as a service the two commands are:

```bash
sudo -u roadie roadie enroll https://station.<tailnet>.ts.net --code ABCD-1234-7F3A
sudo systemctl restart roadie
```

Where the rig has to be depends on the mode:

- A LAN station is reached from its own network only.
- A tailnet station is reached from the tailnet only, so the rig's Pi
  runs Tailscale and is joined to the same tailnet.
- A public station is reached by any rig that can open an outbound
  HTTPS connection.

Roadie's [How to Run
Roadie](https://github.com/VibiumDev/roadie/blob/main/docs/how-to/run-roadie.md)
has the details under "Join a station". The same page covers reaching
Roadie directly on the tailnet with `--auth tailscale` and HTTPS through
`tailscale serve` or `tailscale cert`, which works beside a station: the
rig keeps its tunnel to the station for everyone else while people on
the tailnet open its own `/view`.

Then check the whole path from another machine:

```bash
station-probe -station https://station.<tailnet>.ts.net -token stk_... -device RIG/DEVICE -ice all
```

It takes a hold, connects over WebRTC, watches the picture for ten
seconds, taps, and exits non-zero when anything fails. `-ice all` is
for a tailnet or LAN station, which runs no TURN. Leave it out for a
public station, and the probe sends the media through TURN only. Add
`-insecure` for a LAN-mode station's self-signed certificate.

## Docker

No image is published yet, so build one from the clone. A release will
publish it as `ghcr.io/vibiumdev/station`.

```bash
docker build -t vibium-station .
```

The image holds the same binaries and Vibium 26.8.21, which step 1
describes, with the data directory at `/data`. A public station:

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
sudo install -m 755 station station-probe /usr/local/bin/
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
- `station-probe` fails at the hold: the token is read-only, or someone
  else holds the device.
- The sign-in page refuses a right password: five misses from one
  address lock that address out for up to 15 minutes, and each miss
  after the second makes that login wait longer.
- The authenticator's codes are refused: the station's clock or the
  phone's is off. `timedatectl` says whether the station's is
  synchronized.
- A forgotten password: this sets one at once, for the owner or for
  the login named after `passwd`.

  ```bash
  sudo -u station station passwd --data-dir /var/lib/station
  ```

- A lost authenticator: sign in with a backup code, or have another
  admin hold Reset sign-in on your row under Users on Settings. For an
  owner with neither, this sets a password and forgets the
  authenticator and the backup codes, so the next sign-in sets up an
  authenticator again.

  ```bash
  sudo -u station station passwd --reset --data-dir /var/lib/station
  ```
