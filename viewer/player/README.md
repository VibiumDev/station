# The Record Player, embedded

The files here are the custom-element build of the
[Vibium Record Player](https://github.com/VibiumDev/record-player) (MIT),
`vibium-record-player.js` with the assets it loads beside it, served by
the station at `/viewer/player/` so the Records page can play a
recording in place. `VERSION` names the player commit they were built
from. To update them, run `scripts/update-player.sh` from the repository
root; it clones the player, builds the element and copies the output
here. Nothing in this directory is edited by hand.
