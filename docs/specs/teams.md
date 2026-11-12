# Teams: users, admins and what is shared

The station began single-player: one owner with a password, and in
tailnet mode a short list of identities, every one of them an admin who
sees and may do everything. That is right for one person and their Pis
and wrong the moment a second person signs in, since there is no way to
show them one device and not another, or their own jams and not yours.
This spec adds users and teams to the station binary itself. The
boundary that mattered stays: no vendor accounts, no email, no billing,
nothing a hosted service would need. A self-hosted station still has one
operator; that operator can now give other people logins and decide what
each of them sees.

## The two kinds of people

An **admin** sees and manages everything: every device, jam and record,
Settings, rigs, tokens, users and teams. A **regular user** sees only
what has been shared with a team they belong to, plus what they made
themselves. Nothing is shared by default: a new user who belongs to no
team, or whose teams have been given no devices, sees an empty Devices
page, no jams but their own, and no records but their own uploads and
the public ones.

The owner is the first admin: the password set with `station passwd`
belongs to a user whose login is `owner`. In tailnet mode, identities on
`--allow` are admins too, as the operator's bootstrap trust, and an
identity that an admin has added as a user signs in as that user,
regular or admin as the record says, without appearing in `--allow`. A
user with no name takes the display name their tailnet profile gives;
a name an admin set stays.

## Teams and what a team may do

A team is a named group of users; a user may be in several teams, and
admins are in all of them without being listed. A device is shared with
a team at one of three levels, each including the ones before it:

| Level | May |
|---|---|
| `watch` | Open the watching page, see the device's jams and records |
| `drive` | Take the device and drive it from the viewer |
| `jam` | Start jams and drive the device with an agent over the API |

Sharing follows the device: whoever may watch a device may see the jams
run on it and the recordings made of it. Jams and records are not
shared one by one; sharing the device is the act of sharing, and
unsharing the device hides its history again. Public records stay
reachable by their link, as before, for whoever has the file.

A regular user at `drive` may take a device another regular user holds,
as people recall agents today, and a jam or an agent takes a device a
watcher holds at once, as on any station. Admins' holds are recalled by
admins alone. Device administration over the relay, rotating a phone or
changing a rig's capture, is for admins.

## Signing in

The sign-in page asks for a login and a password, and then for a code
from an authenticator app or a backup code (`second-factor.md`). A
login is an email address as a rule, kept lowercased, and the password
is argon2id-hashed in the database. Sign-ins are throttled per address
and per login. An admin adds a user on Settings and gets a single-use
setup link to pass on, where the user chooses their own password and
adds an authenticator; nobody chooses a password for anyone else. Every
user changes their own on their Account page. `station passwd [login]`
sets a password from the machine itself, for the owner when nobody else
can sign in, and `STATION_PASSWORD` in the environment does the same for
the owner at start, as on Fly.

API tokens belong to a user and act as that user, within a level of
their own: `watch` only looks, `drive` holds and drives devices and runs
jams and agents, as the user's teams allow on each device, and `admin`
manages the station too. A token's level never exceeds its user's own:
admin for an admin, drive for a user with drive or jam on some device,
watch for one who may only watch, and no token at all for a user given
nothing yet. Everyone makes and revokes their own tokens on their
Account page; an admin makes anyone's on Settings. Drive is the default
and what most tokens need. A token minted for a jam is bound to its
device, at drive, as before. Bound means everywhere: it sees that
device's jams and records alone, changes that device's records alone,
and lists nothing by key, whoever its user is.
A record uploaded through a token belongs to the token's user as
much as to the token: the person deletes, shares and refiles it in
person or through any drive token of theirs, and a watch token changes
nothing, not even its user's own.

## Settings

A Teams section on Settings, for admins:

- **Users**: login, name, admin or regular, the teams they are in, how
  they sign in, last sign-in. Add a user with a login, a name and
  whether they are an admin, and get their setup link. Make a user an
  admin or a regular user, lock and unlock, reset their sign-in (held
  for a moment; a new setup link), and remove (held).
- **Teams**: name and members. Add a team, add and remove members,
  remove a team (held).
- **Device access**: a grid of every known device against every team,
  each cell the team's level on that device, none by default, changed
  on the spot.

Every change is audited.

## Data

Four tables beside the ones the station has: `users` (id, login, name,
password hash, admin, locked, created, last sign-in, and the second
factor's columns), `teams` (id,
name), `team_members` (team, user) and `device_access` (device, team,
level). API tokens gain the user they belong to. The owner user is
seeded from the config file's password hash the first time a station
with that hash starts without any users, and existing tokens belong to
the owner. Live state in ordinary tables, as the rule has it; no events
are signed for users or teams.

## API

| Method and path | What it does |
|---|---|
| `GET /v1/users` | Every user, for admins |
| `POST /v1/users` | `{"login", "name", "admin"}`: a new user and their `setup_url` |
| `PUT /v1/users/{id}` | Change `name`, `admin` or `locked` |
| `POST /v1/users/{id}/reset` | Forget their sign-in; a new `setup_url` |
| `DELETE /v1/users/{id}` | Remove a user; their tokens go with them |
| `GET /v1/teams` | Every team with its members |
| `POST /v1/teams` | `{"name"}`: a new team |
| `DELETE /v1/teams/{id}` | Remove a team and its access |
| `PUT /v1/teams/{id}/members/{user}` | Add a member |
| `DELETE /v1/teams/{id}/members/{user}` | Remove a member |
| `GET /v1/access` | Every team's level on every device |
| `PUT /v1/access` | `{"device", "team", "level"}`: set a level, `""` to take it away |

Everything here needs an admin. `GET /v1/devices`, `GET /v1/jams` and
`GET /v1/records` answer with what the caller may see.

## Gate

Two users on the tailnet station: an admin and a regular user in a team
given one device at `drive`. The regular user signs in with login and
password, sees that device alone on Devices, can watch and drive it but
not start a jam, sees the admin's jams on that device and none on a
device they were not given, and sees the device's recordings. An admin
sees everything and manages users and teams on Settings. A user in no
team sees nothing but Jams and Records with their own things.
