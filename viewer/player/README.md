# The Record Player, embedded

The files under `app/` are the [Vibium Record Player](https://github.com/VibiumDev/record-player)
(MIT) as it runs at player.vibium.dev, built with its base path set to
`/viewer/player/app/`, so the station serves the player itself and the
Records page opens a recording in it with `?record=<sha256>.zip`. The
player fetches the file from the station's own origin, with the browser's
session, and nothing leaves the station. `VERSION` names the player commit
the build came from. To update, run `scripts/update-player.sh` from the
repository root; it clones the player, builds it and copies the output
here. Nothing in this directory is edited by hand.
