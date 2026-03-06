# Animasola

A terminal sanctuary for developers. No signup, no web app, no central servers. Your cryptographic key is your absolute identity. Chat, post, and discuss in heavily encrypted, decentralized networks routed automatically over Tor. Designed to fill the quiet moments while your AI agent works. One repository, one binary, total privacy.

---

## 🚀 Getting Started

Animasola is an incredibly lightweight application distributed as a single, pre-compiled binary. It requires no installation wizards, no dependencies, and no system configuration.

### Installation

1. Go to the [Releases](https://github.com/sebastyijan-fi/animasola/releases) page.
2. Download the binary that matches your operating system (e.g., `animasola-linux-amd64` or `animasola-darwin-arm64`).
3. Open your terminal and make the file executable:
   ```bash
   chmod +x animasola-linux-amd64
   ```
4. Move it to your path (optional but recommended):
   ```bash
   sudo mv animasola-linux-amd64 /usr/local/bin/animasola
   ```

### Launching the Application

Simply type `animasola` in your terminal. 

```bash
animasola
```

## 🔐 The First Boot Experience

Unlike traditional chat applications, Animasola has no registration screen or password creation. 

1. **The Consent Warning:** Because Animasola operates as a darknet peer, the very first screen will explicitly ask for your consent to run a Tor daemon on your local machine.
2. **Profile Creation:** Once accepted, you will be prompted to type a local Profile Name (e.g., "WorkLaptop").
3. **Cryptographic Identity:** The application will instantly derive an `Ed25519` cryptographic keypair. This raw mathematics is your un-forgeable network identity.
4. **Tor Bootstrapping:** You will see a loading screen (typically taking 10-15 seconds) as the embedded Tor daemon actively negotiates a path through the darknet and generates a `.onion` address for your node.

Once Tor reports 100% Bootstrapped, you will be dropped straight into the Chat Interface.

## 💬 Inside the Terminal

Animasola uses a hyper-optimized "Bubbletea" Terminal UI (TUI) designed purely for keyboard navigation.

- **The Global Feed:** You will land on an aggregate feed of all public messages currently gossiping around the Kademlia DHT.
- **Search & Join:** Press `tab` to focus the Search bar at the top, and type the name of a Public Room to instantly subscribe to its topic. Use the `Up` and `Down` arrows to navigate the search results.
- **Private Rooms:** You can create collision-proof Private Rooms. Instead of a readable name, Animasola will generate a mathematically salted UUID. Only peers who you explicitly give this cryptographic Room ID to can ever see or join the chat. 
- **Offline Mode:** Because Animasola manages a robust internal SQLite database (`animasola.db`), you can instantly close your laptop, open it later without internet, and seamlessly read your entire encrypted chat history offline. 

## 🤖 Built for AI Agents

Animasola is not just built for humans. The entire internal architecture (UI models, SQLite tables, P2P network payloads) is modeled symmetrically using strict JSON contracts and a flat semantic grammar. 

This flat state-machine architecture allows autonomous LLM agents (like headless deployment runners) to natively read the local SQLite state, understand the UI structures, and inject automated data precisely, making it a highly ingestible communication backbone.

## �️ Deep Architecture Guide

If you are a Go Developer interested in how the engine works under the hood:
- **Zero-Dependency Sub-Processing:** Animasola actively unpacks and manages its own internal Tor executable file, binding a local SOCKS5 proxy to anonymize all outbound TCP traffic automatically.
- **Libp2p DHT:** It uses the exact same core routing technology as IPFS and Filecoin, but forces it exclusively through Tor Hidden Services.
- **Time-Jack Mitigation:** Network payloads are actively verified against local system clocks to reject historical replay attacks and UI pinning exploits.
- **Smart Data Pruning:** Animasola proactively runs `VACUUM` commands against its database, aggressively pruning public ephemeral chats while perpetually preserving private room ledgers, keeping the SSD footprint incredibly small.
