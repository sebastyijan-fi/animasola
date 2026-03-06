# Animasola

A terminal sanctuary for developers. No signup, no web app, no central servers. Your cryptographic key is your absolute identity. Chat, post, and discuss in heavily encrypted, decentralized networks routed automatically over Tor. Designed to fill the quiet moments while your AI agent works. One repository, one binary, total privacy.

## Usage

```bash
animasola
```

Animasola operates entirely over the Tor anonymity network by running a bundled Tor binary. It generates local Ed25519 cryptographic keys the first time you boot the application, meaning your identity is bound exclusively to the machine you are running it on.

## Features

- **Zero-Signup Identity**: Automatically generates Ed25519 keypairs. Your cryptographic key is your username.
- **True Decentralization**: Leverages the Kademlia DHT inside Libp2p over Tor hidden services. There are no central servers.
- **Private Rooms**: Collison-proof, salted UUID private chat rooms secured heavily so only people with the explicit Room ID and Password can connect.
- **Compacting Storage**: A built-in SQLite database manager tracks global state and actively vacuums and prunes public channel data to maintain an extremely small SSD footprint.
- **Terminal UI**: Bubbletea-driven interface optimized for fluid keyboard navigation. Scroll search results without losing keystroke inputs.
- **Built for AI Agents**: All components are structured symmetrically using a flat semantic grammar, making it incredibly ingestible for autonomous LLM expansion.
