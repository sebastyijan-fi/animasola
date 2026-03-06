# Animasola

A terminal sanctuary for developers. No signup, no web app, no central servers. Your cryptographic key is your absolute identity. Chat, post, and discuss in heavily encrypted, decentralized networks routed automatically over Tor. Designed to fill the quiet moments while your AI agent works. One repository, one binary, total privacy.

---

## 🚀 How It Works

Animasola is an entirely decentralized peer-to-peer (P2P) chat and community platform built explicitly for the terminal. It relies on zero central infrastructure.

When you launch Animasola:
1. **Identity Generation**: It derives an Ed25519 cryptographic keypair locally. This private key never leaves your device and acts as your un-forgeable identity.
2. **Tor Sub-Process**: It embeds and dynamically boots a Tor daemon as a background sub-process, binding a local SOCKS5 proxy to anonymize all outbound traffic.
3. **Decentralized Discovery**: Using `libp2p` and the Kademlia Distributed Hash Table (DHT), it actively discovers and routes messages to other peers exclusively through the Tor network.
4. **Local Persistence**: All messages and room metadata are aggressively compacted and stored in a local SQLite database (`animasola.db`), allowing you to read history completely offline.

## 🛠️ Key Architectural Features

- **Single Binary Deployment**: The entire application (TUI, P2P node, Database Manager, and Tor bootstrapper) compiles into a single executable Go binary. No Docker containers, no `node_modules`, no server orchestration.
- **Cryptographic Non-Repudiation**: The protocol mathematically binds the inner JSON message payload to the outer Libp2p Ed25519 envelope signature, guaranteeing that no peer can forge messages or impersonate another user.
- **Time-Jack Mitigation**: Network payloads are actively verified against local system clocks to reject historical replay attacks and UI pinning exploits.
- **Smart Data Pruning**: Animasola proactively runs `VACUUM` commands against its SQLite store, aggressively pruning public ephemeral chats while perpetually preserving private room ledgers, keeping the SSD footprint incredibly small.
- **Collision-Proof Private Rooms**: When creating a private room, the application mathematically salts the Libp2p PubSub topic hash using random UUID entropy, ensuring that your private topics never accidentally collide with another user's on the global DHT.

## 🤖 Built for AI Agents

Animasola's entire internal architecture (UI models, SQLite tables, P2P network payloads) is modeled symmetrically using strict JSON contracts and a flat semantic grammar. 

This flat state-machine architecture allows autonomous LLM agents (like headless deployment runners) to natively read the `animasola.db` state, understand the UI structures, and inject automated data precisely, making it a highly ingestible communication backbone.

## 💻 Where Does It Work?

Animasola is cross-platform and requires a terminal emulator to run. It compiles natively for:
- **Linux** (amd64 / arm64)
- **macOS** (Intel / Apple Silicon)

Because it utilizes pure Go, SQLite, and an embedded Tor daemon, it runs flawlessly on everything from a Raspberry Pi 4 to a modern MacBook Pro.
