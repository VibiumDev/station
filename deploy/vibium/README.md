# A Vibium you built

The station's image downloads a Vibium nightly by default. To put a Vibium
you built into the image instead, such as one from a branch:

```bash
scripts/vibium-local.sh ../vibium   # builds vibium-linux-amd64 and -arm64 here
```

Then add `--build-arg VIBIUM_LOCAL=1` to `docker build` or `fly deploy`.
The binaries stay out of git. See
[Set Up Jams: your own build](../../docs/how-to-guides/set-up-jams.md#your-own-build).
