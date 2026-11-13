# First Steps on a New Station

These steps take a station that has just started, on a host of your own
([Self-host a station](self-host.md)) or on Fly.io
([Deploy to Fly.io](deploy-to-fly.md)), to a phone you can drive and
share with others.

A few commands run on the station's machine, and they are written
differently on each:

- Self-hosted: `sudo -u station station COMMAND --data-dir /var/lib/station`
- Fly.io: `fly ssh console -a NAME -C "station COMMAND"`

## 1. Sign in

Open the station's address:

- Tailnet mode: `https://station.<tailnet>.ts.net`. A login on `--allow`
  is signed in at once, as an admin.
- LAN mode: `https://<the machine's address>:8443`. The browser warns
  about the station's own certificate.
- Public mode: `https://station.example.com`, or `https://NAME.fly.dev`
  on Fly.io.

Sign in as `owner`, with the password you set. The first time, the page
shows a QR code:

1. Scan it with an authenticator app, such as the one built into your
   phone's password manager.
2. Type the six-digit code the app shows.
3. Keep the ten backup codes the station shows next. It shows them once,
   and each one signs you in once when the authenticator is out of reach.

Every later password sign-in asks for a code from the app. The codes
follow the clock, so the station's clock has to be right.

## 2. Enroll a rig

On **Settings → Rigs**, press **New enrollment code**. (On the station's
machine, `station enroll` prints one too.) A code works once, for 15
minutes. Then, on the rig, a Pi where Roadie runs as a service:

```bash
sudo -u roadie roadie enroll https://STATION --code ABCD-1234-7F3A --record
sudo systemctl restart roadie
```

The phone shows on the station's **Devices** page within a few seconds.
`--record` keeps a recording of every session in the station's Record
Store, with video and sound; leave it out if you don't want that.

Where the rig can be depends on the mode:

- LAN mode: on the station's own network.
- Tailnet mode: on the same tailnet, so the Pi runs Tailscale too.
- Public mode and Fly.io: anywhere it can make an outbound HTTPS
  connection.

To give the phone a name on the station's pages, add `--label "Demo
iPhone"` to `ROADIE_ARGS` in `/etc/default/roadie` on the Pi, then
restart Roadie. Roadie's
[How to Run Roadie](https://github.com/VibiumDev/roadie/blob/main/docs/how-to/run-roadie.md)
has the rest, under "Join a station".

## 3. Check the whole path

Make an API token on your **Account** page, or for anyone on
**Settings → Access**. On the station's machine, `station token create
laptop` makes one too. The secret is shown once.

Then, from another machine with a clone of this repository:

```bash
go run ./cmd/station-probe -station https://STATION -token stk_... -device RIG/DEVICE
```

The probe takes the phone, streams it over WebRTC, taps the screen,
checks that the picture changed, and lets the phone go. A non-zero exit
names what failed.

- For a tailnet or LAN station, add `-ice all`: they run no TURN relay.
- For a public station or Fly.io, leave it out: the probe then sends the
  media through TURN only, which proves the relay works.
- For a LAN station, add `-insecure` for its own certificate.

## 4. Jam sessions

To type a prompt and have an LLM carry it out on the phone, with speech
in and out, see [Set Up Jams](set-up-jams.md).

## 5. Let someone else use the phone

A regular user sees nothing until one of their teams is given a device.
On **Settings → Access**:

1. **Teams**: add a team, such as `Acme`.
2. **Users**: add each person, with their email address as the login and
   their name. For a trial or a demo, give the account a date under
   **Expires**, and at that time it stops working on its own (the time
   starts at 12:01 AM). **Add user** shows a setup link once, good for 24
   hours. Send it through a channel you trust.
3. **Teams**: add each of them to the team.
4. **Device access**: give the team a level on the phone. **Watch** only
   watches, **drive** also takes control from the viewer, and **jam** also
   starts jam sessions.

Tell each person to open the link within 24 hours, choose a password,
scan the QR code with an authenticator app, and keep the backup codes the
station shows. Then they open **Devices** and pick the phone.

To stop someone sooner, hold **Lock** in the **⋯** menu on their row, which
also takes the phone from them at once. **Remove** deletes the account.

## If you're locked out

- **Too many tries**: five wrong passwords from one address lock that
  address out for up to 15 minutes.
- **A lost authenticator**: sign in with a backup code, or have another
  admin hold **Reset sign-in** on your row under **Settings → Access →
  Users**. That makes a new setup link.
- **A forgotten password, self-hosted**: this sets a new one at once, for
  the owner or for the login you name after `passwd`:

  ```bash
  sudo -u station station passwd --data-dir /var/lib/station
  ```

  For an owner with no backup code and no other admin, `passwd --reset`
  also forgets the authenticator and the backup codes, so the next sign-in
  sets up an authenticator again.
- **A forgotten password, Fly.io**: the owner's password is the
  `STATION_PASSWORD` secret. Set it again and redeploy. For the
  authenticator, run `station passwd --reset` in `fly ssh console -a NAME`.
