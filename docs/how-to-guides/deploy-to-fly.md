# Deploy a station to Fly.io

This guide puts a public Vibium Station on [Fly.io](https://fly.io). It
runs on a single machine, with TLS ending at Fly's edge and the
station's data kept on a volume, while the TURN relay listens on the
app's own IPv4 address. The guide assumes you have a Fly account, that
the `fly` command is signed in, and that you have a clone of this
repository.

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
dedicated address, and Fly bills it separately; note the address that
`fly ips allocate-v4` prints, because the configuration below needs it.
The volume holds the station's configuration, its key and its database
so that they survive deploys and restarts, and one gigabyte is plenty.

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

Leave the rest as it is. It runs the station on plain HTTP on port 8080
behind Fly's TLS, binds TURN to `fly-global-services:3478` with relay
ports 49152 to 49215 on the same address, keeps one machine always
running so that the rigs' tunnels stay up, and mounts the volume at
`/data`.

## 4. Deploy

From the repository root:

```bash
fly deploy -c deploy/fly/fly.toml --dockerfile Dockerfile --remote-only --ha=false -a NAME
```

`--ha=false` keeps the app to one machine, which both the volume and the
station's in-memory queue of holds require. The first deploy builds the
image on Fly and takes a few minutes; `fly logs -a NAME` shows the
station starting, and on the first start it prints `Password set from
STATION_PASSWORD`. The image carries the Vibium binary too, for jams:
once the station is up, a model added on Settings turns Jams on.

## 5. Check it

Open `https://NAME.fly.dev` and sign in with the password. Settings
shows the station's name, its key and the TURN line, which should carry
the public address from step 1.

Then enroll a rig: make an enrollment code on Settings and run
`roadie enroll https://NAME.fly.dev --code ABCD-1234-7F3A` on the rig,
which then appears on the front page within seconds.

To prove the relay path end to end, make an API token on Settings and
run the probe from your machine, which uses relay-only media by
default:

```bash
go run ./cmd/station-probe -station https://NAME.fly.dev -token stk_... -device RIG/DEVICE
```

The probe takes the device, streams over WebRTC through TURN, taps the
screen, checks that the picture changed, and releases the device; a
non-zero exit names what failed.

## 6. Use your own domain

`NAME.fly.dev` works as it is, and Fly keeps serving it after you add a
domain of your own, so this step can wait. Settle the name before
enrolling rigs, though, because a rig remembers the address it enrolled
with. Rigs enrolled under the old name keep working, and moving one is a
matter of running `roadie enroll` again with the new address, which
keeps its rig id.

Ask Fly for a certificate first, since its edge only routes a custom
domain that has one, even for plain HTTP:

```bash
fly certs add station.example.com -a NAME
```

The command prints the DNS records to create at your registrar. For a
subdomain such as `station.example.com`, A and AAAA records pointing at
the app's addresses are the sturdy choice; `fly ips list -a NAME` shows
them, and the IPv4 is the dedicated one from step 1, which TURN already
uses. A CNAME to the app's `.fly.dev` hostname works for a subdomain
too, but an apex such as `example.com` should get A and AAAA records
unless your DNS provider flattens CNAMEs. Fly validates the domain on
its own once the records resolve, and `fly certs check
station.example.com -a NAME` shows how far it has got. A wildcard
certificate, or one issued before any traffic reaches the app, needs
the `_acme-challenge` CNAME that the same output lists.

Then tell the station its new name. In `fly.toml`, change `--public-url`
to `https://station.example.com` and deploy again. Leave
`--turn-public-ip` as it is, because TURN is reached by address rather
than by name, and TLS still ends at Fly's edge, so the station's own
`--public` flag stays out of the picture. From then on the pages, the
enrollment codes and the tunnel address the station gives new rigs all
carry the new name.

## Afterwards

- **Updating**: run the deploy command again. The volume keeps the
  configuration, the key and the database, while live holds and
  sessions end with the restart and the rigs reconnect on their own.
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
