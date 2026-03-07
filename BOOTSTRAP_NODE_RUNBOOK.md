# Bootstrap Node Runbook

This is the minimum operator flow for bringing up the first Tor-only Animasola bootstrap node.

## Goal

Start one seed node that:

- runs Tor locally
- exposes an onion address
- starts the Animasola libp2p node in bootstrap-operator mode
- prints a bootstrap address other nodes can use

## Requirements

- a machine that can run Animasola continuously
- Tor available through the bundled release or `ANIMASOLA_TOR_BIN`
- the `bootstrap-node` command built from this repo

## Start the First Bootstrap Node

Run:

```bash
go run ./cmd/bootstrap-node --profile bootstrap-a --listen-port 4001 --socks-port 45000
```

What this does:

- creates or reuses the profile at `~/.config/animasola/bootstrap-a`
- starts Tor
- creates a hidden service for the libp2p listener
- starts the node with `AllowEmptyBootstrap` enabled so the first seed can exist before any peers are configured

When startup succeeds, it prints something like:

```text
Bootstrap node ready
Profile: bootstrap-a
Peer ID: <peer-id>
Onion bootstrap address: /onion3/<56-char-onion>:4001/p2p/<peer-id>
```

That final `/onion3/.../p2p/...` value is the bootstrap address you distribute to every other node.

## Configure Normal Nodes

On every non-bootstrap node, set:

```bash
export ANIMASOLA_BOOTSTRAP_PEERS="/onion3/<56-char-onion>:4001/p2p/<peer-id>"
```

If you run more than one bootstrap node:

```bash
export ANIMASOLA_BOOTSTRAP_PEERS="/onion3/<onion-a>:4001/p2p/<peer-a>,/onion3/<onion-b>:4001/p2p/<peer-b>"
```

Then start Animasola normally.

## Validate

Check the environment and Tor-only requirements with:

```bash
ANIMASOLA_BOOTSTRAP_PEERS="/onion3/<56-char-onion>:4001/p2p/<peer-id>" \
ALL_PROXY="socks5://127.0.0.1:45000" \
animasola doctor
```

You want `doctor` to report:

- `Tor: ok`
- `Tor SOCKS proxy: ok`
- `Bootstrap peers: ok`

## Operating Notes

- The first bootstrap node is special only because it starts with `AllowEmptyBootstrap`.
- Normal user nodes should not use empty bootstrap configuration.
- Keep the bootstrap node stable. If it changes onion address or peer ID, every client bootstrap config must be updated.
- Run at least two bootstrap nodes before calling the network stable.

## Recommended Next Step

After `bootstrap-a` is live, start `bootstrap-b` with `ANIMASOLA_BOOTSTRAP_PEERS` pointing at `bootstrap-a`, then update clients to use both nodes.
