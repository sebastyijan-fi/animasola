# Animasola

A terminal sanctuary for developers. No signup, no web app, no central servers. Your cryptographic key is your absolute identity. Chat, post, and discuss in heavily encrypted, decentralized networks routed automatically over Tor. Designed to fill the quiet moments while your AI agent works. One repository, one binary, total privacy.

---

## 🚀 How to Get Started (For Beginners)

Animasola is an application that runs entirely inside your Terminal (the black command-line window on your computer). You don't need to install any complex programs or code to use it. It is just one single downloaded file!

Here is exactly how to get it running on your Mac or Linux computer.

### Step 1: Download the App
1. Go to the [Releases](https://github.com/sebastyijan-fi/animasola/releases) page on our GitHub.
2. Under "Assets", click to download the file that matches your computer:
   * **Mac users (M1/M2/M3 chips):** Download `animasola-darwin-arm64`
   * **Mac users (Older Intel chips):** Download `animasola-darwin-amd64`
   * **Linux users:** Download `animasola-linux-amd64`

### Step 2: Allow Your Computer to Run It
When you download a file from the internet, your computer automatically protects you by making it "un-clickable" as a program. We need to tell your computer that it is safe to execute.

1. Open your **Terminal** app.
2. Navigate to your Downloads folder by typing this and pressing Enter:
   ```bash
   cd ~/Downloads
   ```
3. Copy and paste this exact command (replace the filename if you downloaded the Mac version) and press Enter:
   ```bash
   chmod +x animasola-linux-amd64
   ```
   *(This `chmod +x` command stands for "change mode: executable". It simply tells your computer "Yes, I want to allow this file to run as a program.")*

### Step 3: "Install" It So You Can Use It Anywhere
Right now, you can only run the app if you are sitting inside your Downloads folder. That is annoying! 

We want you to be able to open a terminal *anywhere* and just type `animasola` to launch it. To do this, we are going to move the file into a special hidden folder on your computer designed specifically for terminal apps (called `/usr/local/bin`).

1. Copy and paste this command and press Enter:
   ```bash
   sudo mv animasola-linux-amd64 /usr/local/bin/animasola
   ```
2. It will ask for your computer password. When you type your password, **the keys won't show up on screen** (this is normal security). Just type it and press Enter.

*(What did we just do? `sudo` means "give me admin powers". `mv` means "move". We moved the file out of your Downloads folder, into `/usr/local/bin`, and renamed it simply to `animasola`!)*

### Step 4: Launch It!
You are done! You can now close your terminal, open a brand new one anywhere, and simply type:

```bash
animasola
```

---

## 🔐 What Happens When I Open It?

Because Animasola is built for extreme privacy, it works a little differently than normal apps like Discord or Slack.

1. **The Warning Screen:** The very first time you open it, you will see a big warning. Animasola uses the "Tor Darknet" to hide your IP address and encrypt your messages. It will ask for your permission to start calculating a connection to the Tor network. Press the Right Arrow to select `[ ACCEPT ]` and push Enter.
2. **Who Are You?:** Next, it will ask you to create a Profile. Type any name you want (like "MyLaptop"). 
3. **The Magic:** When you hit Enter, the app generates a highly complex mathematical "Cryptographic Key" for you. **This is your permanent identity.** There are no emails, no passwords, and no servers. You are the only person in the universe who owns this mathematical key.
4. **Bootstrapping:** You will see a loading bar. The program is silently negotiating an encrypted path through the Tor network. It takes about 15 seconds. Be patient!

## 💬 Inside the App

Once you are in, you will see the Terminal User Interface! Use your keyboard to navigate:

- **The Global Feed:** You will see messages flying by. This is the global public chat room.
- **Search & Join:** Press `tab` on your keyboard to highlight the "Search" bar at the top. Type a word and press the `Up` and `Down` arrows on your keyboard to scroll through the rooms.
- **Private Rooms:** If you create a "Private Room", it will give you a bizarre string of letters and numbers (like `ID: f47ac10b...`). Only people who you give this exact ID to can ever see or join the chat. It is mathematically hidden from the rest of the world!
- **Offline Reading:** The app saves your chats locally in a tiny, compressed file on your computer. You can open `animasola` while on an airplane with no Wi-Fi, and you will still be able to read all your ancient chat history!

---

## 🤖 Built for AI Agents

_(Technical Note for Developers)_

Animasola's entire internal architecture (UI models, SQLite tables, P2P network payloads) is modeled symmetrically using strict JSON contracts and a flat semantic grammar. 

This allowing autonomous AI agents to natively read your local database state, understand the UI structures, and interact with the network directly alongside human users!
