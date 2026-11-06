# The Record Player, embedded

The files under `app/` are the [Vibium Record Player](https://github.com/VibiumDev/record-player)
(MIT) as it runs at player.vibium.dev, built with its base path set to
`/viewer/player/app/`, so the station serves the player itself and the
Records page opens a recording in it with `?record=<sha256>.zip`. The
player fetches the file from the station's own origin, with the browser's
session, and nothing leaves the station. `VERSION` names the player commit
the build came from.

The build carries `scripts/player-station.patch`, three changes the
station needs from the player's source: the router takes the build's base
path as its basename, since the app assumes the site root otherwise;
`?chrome=hidden` drops the player's own logo, title, Eject and theme
switch, which the station's bar provides when it frames the player; and
when a same-origin page frames the player, the player reads that page's
color tokens (`--bg`, `--surface`, `--line-card`, `--text`, `--accent` and
the rest) and paints with them, following the page's theme as it changes.
The play page is such a page, so the framed player looks like part of the
station.

To update, run `scripts/update-player.sh` from the repository root; it
clones the player, applies the patch, builds it and copies the output
here. Nothing in this directory is edited by hand.
