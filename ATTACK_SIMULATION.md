# Attack Simulation

This repository now includes a standalone red-team lab that exercises real nodes without touching core service files.

Run:

```bash
./scripts/attack_simulation.sh
```

Run selected scenarios:

```bash
./scripts/attack_simulation.sh --transport local --scenarios spoofed-discovery,snapshot-flood
```

The wrapper isolates all config, Tor state, identities, and SQLite files under a temporary `HOME`/`XDG_CONFIG_HOME` so the lab does not pollute normal Animasola profiles.

## Transport modes

`local`
- real loopback sockets
- no Tor dependency
- good for protocol and abuse-path simulation
- this is the current default

`tor`
- real Tor-backed node startup
- intended for full Option A validation
- currently blocked on this machine by the Linux bundled Tor artifact shipping only the `tor` binary without its required shared libraries

Run Tor mode explicitly:

```bash
./scripts/attack_simulation.sh --transport tor --scenarios spoofed-discovery
```

## Local attack scenarios covered

`spoofed-discovery`
- attacker publishes a forged global discovery announce
- creator id and signature are invalid
- expected result: victim ignores it

`snapshot-flood`
- attacker sends repeated snapshot requests against a victim with a joined public room
- expected result: victim rebroadcasts stay bounded by cooldown

`spoofed-chat`
- attacker joins a real room and publishes a raw forged room message with a mismatched `AuthorID`
- expected result: victim rejects it and does not store it

`message-flood`
- attacker joins a real room and sends a rapid burst of valid messages from one sender
- expected result: victim stores only a bounded subset because inbound per-author burst limiting holds

`multi-sender-flood`
- multiple attackers join a real room and each send a rapid burst of valid messages
- expected result: victim stores only a bounded subset because room-level aggregate burst limiting holds

`metadata-churn-flood`
- a public-room owner rapidly renames and republishes the same room metadata
- expected result today: likely `EXPOSED` unless the owner update path enforces a cooldown

`duplicate-display-name`
- two different peers send messages using the same display name
- expected result today: one peer can collide with the other on the local `users.username` uniqueness constraint

`username-rewrite`
- one peer sends messages under one display name and then another
- expected result today: older messages render under the latest name because usernames are stored per peer, not per message

`public-room-spam`
- one peer creates many distinct public rooms
- expected result today: victims discover them

`private-room-spam`
- one peer creates many private rooms
- expected result: they remain local/private and do not leak into public discovery

`sybil-room-flood`
- multiple attacker identities create and announce public rooms
- expected result today: victim sees them
- this is intentionally reported as `EXPOSED`, not `PASS`, because open public discovery is still Sybil-exposed by design

## Attack vectors identified

### Simulated locally now
- forged public discovery metadata
- snapshot request flooding
- forged room message authorship
- public room Sybil flooding

### Identified but not simulated in this local lab
- bootstrap onion service DoS at the VPS/network edge
- bootstrap host compromise
- release-signing key theft
- GitHub/release workflow compromise
- typo-squat and fake-download distribution attacks
- upstream dependency supply-chain poisoning

These require either real infrastructure, external traffic shaping, or supply-chain drills rather than local node simulation.

## Result model

The lab prints one line per scenario:

- `PASS`: the attack was attempted and the current protections held
- `EXPOSED`: the attack is currently viable by design or policy
- `FAIL`: the lab could not complete or a protection that should have held did not

`EXPOSED` does not fail the lab process. It is there to make the remaining abuse surface explicit.

## Real run results on this machine

Local loopback mode was executed successfully:

- `PASS` forged discovery metadata was ignored
- `PASS` snapshot flooding stayed bounded by cooldown
- `PASS` forged room messages with mismatched authorship were rejected
- `PASS` rapid single-sender room-message floods are bounded by listener-side burst limits
- `EXPOSED` duplicate display names can suppress one peer’s messages through local username collision
- `EXPOSED` a peer can rewrite its displayed name retroactively across older messages
- `EXPOSED` a single peer can flood public discovery with many rooms
- `PASS` private-room spam stayed local and did not leak into public discovery
- `EXPOSED` public room Sybil flooding remains viable in open discovery

Tor-backed mode did not complete because the current Linux bundled Tor runtime is incomplete:

- bundled path: `releases/animasola-linux-amd64/tor/tor`
- observed failure: missing `libevent-2.1.so.7`

That means the attack lab also surfaced a concrete release packaging defect: the Linux Tor bundle is not self-contained enough to support fresh isolated execution on this host.
