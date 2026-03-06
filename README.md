# Animasola

A terminal community platform for developers. SSH in, you're in. No signup, no app, no browser. Your SSH key is your identity. Chat, post, discuss in threaded communities. Fills the quiet moments while your AI agent works. One repo, one binary, self-hostable.

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
