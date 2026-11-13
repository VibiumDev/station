# A Vibium you built

The station's image downloads a Vibium nightly by default. To put a Vibium
you built into the image instead, such as one from a branch:

```bash
scripts/vibium-local.sh ../vibium        # builds vibium-linux-amd64 and -arm64 here
fly deploy --build-arg VIBIUM_LOCAL=1    # or docker build --build-arg VIBIUM_LOCAL=1 .
```

The binaries stay out of git. See
[Deploy to Fly.io](../../docs/how-to-guides/deploy-to-fly.md#use-a-vibium-you-built).
