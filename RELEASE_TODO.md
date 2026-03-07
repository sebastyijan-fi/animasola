# Release TODO

## Current State

Animasola is much closer to a usable release:

- bundled `.tar.gz` releases exist
- `animasola update` works in principle
- `animasola doctor` exists
- startup no longer depends on runtime Tor downloads if releases are bundled correctly
- incompatible old local DBs should now archive/reset instead of crashing

But it is not finished yet.

## Release Risks Still Open

1. Update path is still messy across generations.
Older installed binaries expected raw assets like `animasola-linux-amd64`, while newer releases are bundle-first. Compatibility was patched on the GitHub release side, but the updater story is still transitional.

2. Release assets are inconsistent.
Some releases now contain:
- new bundle assets
- old raw binary assets
- temporary compatibility assets

That works, but it is not clean. A proper stable release line should have one clear asset strategy.

3. No full real-world dogfood pass yet.
This still needs a full "fresh machine / fresh user" run:
- install from release bundle
- launch
- create profile
- chat
- update
- relaunch

4. Tor packaging is still operationally fragile.
The code supports bundled Tor cleanly now, but release production still depends on supplying the correct Tor binary inputs during build. That needs to become a disciplined release step.

5. Old-data handling is pragmatic, not elegant.
If a local DB is incompatible, Animasola now archives/resets it. That is the right call for simplicity given no legacy burden, but it means local history may disappear for users coming from broken alpha builds. This should be documented clearly.

6. Release notes and GitHub assets need cleanup.
Some earlier releases still have mixed old/new assets and older messaging. They should be cleaned so users are not confused by multiple artifact styles.

## Product / UX Work Still Needed

7. Beginner-facing copy still needs one final polish.
It is much better now, but the first-run copy, README intro, and release notes should all consistently say:

"Tor is included for private routing."

without sounding scary or experimental.

8. `doctor` is useful, but still support-oriented.
That is fine, but the normal user should almost never need it. The goal should stay:

- install
- run
- chat

and only use `doctor` if something went wrong.

## Engineering / Quality Work Still Needed

9. Need one true release smoke test in CI or release workflow.
The smoke script exists, but it should become part of the actual release gate, not just a manual script.

10. Need clearer artifact policy.
Decide and stick to one of these:

- bundled archives only
- bundled archives plus compatibility raw binaries for one transition window

Right now it is in between.

11. Need installer/update parity testing.
The installer is bundle-first. The updater now handles bundles, but this should be tested more thoroughly on:

- bundled install
- old raw-binary install
- repeated updates

12. Need a stable versioning/release cadence.
Several alpha releases were cut quickly to unblock testing. That was the right tactical move, but the release line now needs to calm down and become intentional.

## Recommended Next Steps

1. Clean GitHub releases so only the intended assets remain visible.
2. Do a full fresh-machine dogfood pass from published artifact to update flow.
3. Make `scripts/release_smoke.sh` part of the release checklist every time.
4. Freeze the asset strategy for the next few releases.
5. Ship one calm "install/update story" doc for users.

## Blunt Summary

The core app is no longer the main problem.

What is left is release discipline, asset consistency, and real dogfooding from the user's point of view.
