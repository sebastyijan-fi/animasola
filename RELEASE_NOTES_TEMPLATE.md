# Release Notes Template

## Installing Animasola

Animasola runs in your Terminal. Tor is included for private routing, so you do not need to install or configure Tor yourself.

For a normal install, download the `.tar.gz` bundle for your Mac or Linux machine and run the bundled installer.

## Updating Animasola

If you already have Animasola installed, close the app and run:

```bash
sudo animasola update
```

If you installed from the normal release bundles, this is the update path you should keep using.

## Tor-Only Networking Note

Animasola now expects onion-only bootstrap peers for Tor-only networking.

Normal users do not need to type these addresses by hand. User-facing releases should embed the bootstrap peer list directly into the app. Advanced operators who run bootstrap infrastructure can still override the list with `ANIMASOLA_BOOTSTRAP_PEERS` using comma-separated `/onion3/.../p2p/...` addresses.

## Notes for Early Alpha Testers

Very early alpha builds used older install layouts and rougher local database formats.

If Animasola detects an incompatible old local database during startup, it will archive/reset that database instead of crashing. This keeps the app usable, but some old local chat history from broken alpha-era builds may not carry forward.

## Short User Summary

- Download the `.tar.gz` bundle.
- Install once.
- Run `animasola`.
- Update later with `sudo animasola update`.
