# Second factor: setup links, authenticator codes and backup codes

Teams (`teams.md`) gave the station several logins, and with them a
worry: a password an admin typed for someone else and sent along in a
chat is the easiest way into a station. This spec closes that path.
Nobody chooses a password for anyone else, every password sign-in has
a second factor, and an admin can lock a sign-in or reset it. Nothing
here needs a service outside the station: the codes are TOTP as any
authenticator app speaks it, the QR code is drawn by the station, and
the backup codes are printed once.

## Setup links instead of first passwords

Adding a user makes a **setup link**, `/setup/{token}`, single-use and
good for 24 hours, shown once on Settings and returned by the API. The
admin passes it on; the person opens it, chooses their own password,
and sets up an authenticator app from a QR code or the secret as text.
A code from the app confirms the setup, ten backup codes are shown once,
and they are signed in. A link that was used or has expired says so and
sends them to an admin for a new one.

A user with no password and no open link cannot sign in by password at
all; in tailnet mode they still sign in by identity, as before.

## Signing in

The sign-in page asks for the login and the password. When they match,
the station asks for a six-digit code from the authenticator app, or a
backup code. Five wrong codes count like five wrong passwords. A code is
accepted once: the step it was made in is remembered, so a code seen on
the wire cannot be replayed inside its half minute. Each backup code
works once; the Account page says how many are left and makes a fresh
set, which replaces the old one.

A user who has a password but no authenticator yet, the owner from
before this spec among them, is sent to the setup page after the
password and can reach nothing else until a code confirms the
authenticator. Signing out from there is always possible.

Tailnet identities and API tokens are not passwords and carry no second
factor: Tailscale and the token's own entropy do that work.

## Passwords

A password has at least 12 characters, does not contain the login or
the part of it before the `@`, and is not a common word dressed up with
digits. Changing one's own password asks for the current one.

## Slowing a guesser down

Three brakes, as NIST 800-63B and OWASP suggest, none of them a hard
lock on the account:

- **By address**, five misses in 15 minutes lock that address out for
  the rest of the window, as before.
- **By login**, each miss after the first two lengthens the wait before
  the next try at that login is looked at: a second, then two, four,
  doubling up to 15 minutes, whatever address the tries come from.
  Misses are forgotten by a successful sign-in or after a quiet quarter
  hour. A guesser moving between addresses gets a couple of dozen
  tries an hour; a person who mistypes twice feels nothing.
- **Device cookies.** A browser that signs all the way in gets a
  signed cookie naming its user, good for a year. Tries carrying that
  cookie skip the login's wait and are measured by the cookie instead,
  five misses locking it like an address. So a stranger guessing at a
  login never holds its owner up on their own browser, and a stolen
  browser gets no more tries than a stranger. The cookie vouches for
  nothing else: it is not a session and signs nobody in.

Wrong passwords and wrong codes count alike. Every wrong answer waits
half a second before it is refused.

## Admins

On Settings, each user's row says how they sign in: by a setup link
still open, by password and authenticator, by password alone until
their next sign-in, or not at all. An admin can:

- **Lock** and **Unlock** a user. A locked user cannot sign in, their
  open sessions end at their next request, and their tokens stop
  working; their history stays.
- **Reset sign-in** (held for a moment): the password, the
  authenticator and the backup codes are forgotten, open sessions end,
  and a new setup link is made. This is the answer to a forgotten
  password and to a lost phone alike. Tokens are untouched.
- **Remove** (held), as before.

Nobody sets another person's password on the pages or over the API. On
the station's own machine, `station passwd [login]` still sets one, and
`station passwd --reset [login]` also forgets the authenticator, which
is how an owner who lost both phone and backup codes gets back in.
`STATION_PASSWORD` sets the owner's password at start as before; the
owner then sets up an authenticator at their first sign-in.

## Account

Everyone's Account page shows their login, role and teams, the state of
their second factor and the backup codes left. There they change their
password (current password needed), make new backup codes (password
needed) and replace the authenticator (password needed; the new one
must confirm with a code before the old one is forgotten).

## Data

`users` gains `totp_secret` (base32, confirmed), `totp_at` (when it
was confirmed) and `totp_step` (the last step a code was accepted
for). `backup_codes` holds each user's codes as SHA-256 hashes with
`used_at`. `invites` holds setup links as SHA-256 hashes with the user,
who made it and when it expires; a new link replaces a user's older
one. The `disabled` flag is `locked`. Sessions stay in memory and gain
a stage: awaiting a code, setting up, or signed in.

## API

| Method and path | What it does |
|---|---|
| `POST /v1/users` | `{"login", "name", "admin"}`: a new user, 201, with `setup_url` |
| `PUT /v1/users/{id}` | Change `name`, `admin` or `locked` |
| `POST /v1/users/{id}/reset` | Forget their sign-in and answer with a new `setup_url` |
| `DELETE /v1/users/{id}` | As before |

`GET /v1/users` carries `has_password`, `two_factor` and `locked`. There
is no `password` field anywhere.

## Gate

On the tailnet station: an admin adds a user and gets a setup link; the
link sets a password, refuses a weak one, shows a QR code, accepts the
authenticator's code, prints ten backup codes and signs the user in;
the same link refuses a second use. Signing out and in again asks for
a code after the password; a wrong code is refused; a backup code works
once. Lock refuses the next sign-in and ends the open session. Reset
sign-in ends the session and issues a new link. The owner, signing in
by password for the first time since this spec, is taken to the setup
page and gets in after confirming an authenticator.
