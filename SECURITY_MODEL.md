# Animasola Security Model

## Purpose

This document defines the security model Animasola is trying to uphold.

It is not a marketing document. It is a working statement of:

- what Animasola protects
- what it does not protect
- what assumptions it relies on
- what abuse and attack surfaces must be controlled

## Product Position

Animasola is a peer-to-peer terminal chat system with local identity keys and Tor-backed private routing.

The intended product stance is:

- private rooms are the primary safe mode
- public rooms are hostile space
- discovery should become curated by default
- cryptographic identity matters more than account identity
- release/update trust must be verified cryptographically

## Core Security Goals

Animasola should protect:

- user identity ownership
- private room confidentiality
- message authenticity
- release/update integrity
- local data integrity
- user privacy against unnecessary local or network leakage

Animasola should also resist:

- spam
- network degradation
- discovery abuse
- operator mistakes that weaken trust

## Non-Goals

Animasola does not guarantee:

- a spam-free open public network
- that unknown public rooms are safe
- that public metadata is trustworthy without signatures and reputation context
- perfect anonymity against a global adversary
- that third-party clients cannot exist

If the transport and discovery layers are open, outsiders can write their own clients.

The goal is not to make that impossible.

The goal is to make abuse expensive, visible, and containable.

## Trust Boundaries

### 1. Local device boundary

Each user controls a local Ed25519 identity key stored on their device.

If that key is stolen, the attacker can act as that user.

### 2. Private room boundary

Private room confidentiality depends on room key secrecy.

Anyone with the correct private room key can read and write as an admitted room participant.

### 3. Public network boundary

Public rooms and public discovery are untrusted by default.

They must be treated as hostile input.

### 4. Release boundary

The official release-signing key is a root of trust.

If the private signing key is compromised, release/update trust is compromised.

### 5. Bootstrap boundary

Bootstrap nodes are not the network itself, but they are trusted entry points for discovery and initial reachability.

Compromised bootstrap nodes can degrade onboarding and discovery quality.

## Security Invariants

### Identity invariants

- A peer must not be able to impersonate another peer without that peer's private key.
- Discovery metadata must be signed by the room owner identity.
- Message envelopes must bind claimed author identity to the authenticated libp2p peer identity.

### Private-room invariants

- Private room metadata must not leak into public discovery.
- Private room messages must not be readable without the room key.
- Wrong-key or malformed private payloads must fail closed.

### Discovery invariants

- Unauthorized public room metadata updates must be ignored.
- Old metadata must not overwrite newer metadata.
- Discovery responses must be rate-limited and abuse-resistant.
- Unknown public content must be treated as hostile space.

### Transport invariants

- Tor-only mode must fail closed if required bootstrap/routing conditions are missing.
- The node must not silently fall back to mDNS or public clearnet bootstrap paths.
- Outbound onion dialing must use the configured Tor SOCKS endpoint.

### Release invariants

- Auto-update must refuse unsigned or tampered releases.
- The release manifest must be signed.
- Bundles installed by the updater must match the signed manifest.

### Local-storage invariants

- Untrusted network input must not cause unbounded storage growth.
- Incompatible local DBs must not cause crashes.
- Sensitive runtime logs must not be world-readable.

## Threat Model

### Threats Animasola should account for

- malicious peers on the public network
- Sybil/spam identities
- discovery flooding and amplification attempts
- malformed or oversized payloads
- room-name, username, and metadata spoofing
- updater supply-chain tampering
- bootstrap-node compromise or downtime
- local multi-user information leakage from logs or permissions
- phishing around downloads and fake releases

### Threats Animasola should assume are possible

- outsiders can write their own client
- public rooms will attract junk, abuse, and impersonation attempts
- bootstrap nodes can be targeted for DoS
- release infrastructure can be attacked

### Threats Animasola does not fully solve

- a global network observer
- a fully compromised local machine
- a stolen identity key
- human phishing and social engineering

## Abuse Model

### Public rooms

Public rooms are not trusted spaces.

They should be modeled as:

- discoverable
- spam-prone
- reputation-sensitive
- potentially malicious by default

### Private rooms

Private rooms are the meaningful trust domain.

They should be the default recommendation for real user communication.

### Unknown peers

Unknown peers should receive stricter handling than trusted peers.

This should include:

- lower quotas
- tighter rate limits
- less visibility in discovery
- less amplification from operator infrastructure

## Required Abuse Controls

### Discovery controls

- rate limit snapshot responses globally
- rate limit snapshot responses per peer
- cap how much data one snapshot cycle can rebroadcast
- expire stale discovery entries
- ignore unauthorized metadata changes
- eventually add proof-of-work or cost for public-network announcements

### Public-room controls

- require signed room ownership
- reject unauthorized metadata updates
- cap room metadata size
- cap public-room creation and announcement frequency
- eventually support allowlist/blocklist or curated discovery views

### Message controls

- cap message size
- reject malformed payloads
- reject obviously replayed or time-jacked payloads
- deduplicate by message ID
- prevent unbounded async queue growth where possible

### Local-user controls

The client should eventually support:

- mute peer
- block peer
- hide room
- trust peer
- trust room
- mark room as curated or verified

## Discovery Policy Direction

The intended direction is:

- transport can remain open
- public discovery should become curated
- public rooms should not be promoted as inherently safe

This means the long-term default should likely be:

- show trusted or curated discovery first
- show unknown public rooms behind an explicit action
- label hostile-space features honestly

## Operator Responsibilities

Operators are responsible for infrastructure users should never manage manually.

That includes:

- running bootstrap nodes
- keeping bootstrap nodes online
- rotating bootstrap nodes if needed
- embedding trusted bootstrap peers into releases
- protecting the release-signing private key
- publishing signed releases

Operators must not:

- store the release-signing private key on bootstrap VPSes
- treat bootstrap infrastructure as equivalent to release infrastructure
- assume public discovery is self-policing

## Release Security Requirements

User-facing releases should require:

- signed `checksums.txt`
- verified auto-update bundle installation
- a populated embedded bootstrap peer list
- a stable official download path

The release-signing private key should be:

- stored outside git
- access-controlled tightly
- backed up securely
- rotated deliberately, not casually

## Bootstrap Security Requirements

Bootstrap nodes should be:

- Tor-only entry points
- rate-limited at the protocol layer
- hardened at the VPS/network layer
- monitored for uptime and abuse
- separated across providers or regions when possible

At least two bootstrap nodes are recommended for a stable public release.

## UI / Product Honesty Requirements

The product should communicate clearly:

- Tor is included for private routing
- private rooms are the recommended mode for meaningful use
- public rooms are public and potentially hostile
- `doctor` is for troubleshooting, not normal use

Security depends on honest UX framing as much as on protocol mechanics.

## Open Decisions Still To Resolve

- whether public discovery is curated by default
- whether proof-of-work is required for public-network actions
- whether operator trust lists are local-only or distributed
- whether community bootstrap nodes are ever allowed
- whether public rooms are core product or secondary feature

## Practical Default Recommendation

For the next serious release, Animasola should operate under these defaults:

- private rooms are the primary use case
- public rooms are explicitly hostile-space
- auto-update is verified only
- bootstrap peers are embedded in releases
- bootstrap nodes are operator-run
- unknown peers face tighter discovery and amplification limits
- local logs stay private

## Summary

Animasola should optimize for:

- trustworthy private communication
- honest threat boundaries
- abuse-resistant public networking
- cryptographically verified release trust

It should not pretend that an open decentralized public space can be made safe simply by calling it private.
