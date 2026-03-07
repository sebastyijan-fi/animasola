# Animasola

A terminal sanctuary for developers. No signup, no web app, no central servers. Your cryptographic key is your identity. Chat and discuss over a private decentralized network that includes Tor internally, so you do not need to install or manage it yourself.

---

## 🚀 How to Get Started (For Beginners)

Animasola runs entirely inside your Terminal. Tor is included for private routing, so you do not need to install or configure Tor yourself. Download one release bundle, install it, and run `animasola`.

Here is exactly how to get it running on your Mac or Linux computer.

### Step 1: Download the App
1. Go to the [Releases](https://github.com/sebastyijan-fi/animasola/releases) page on our GitHub.
2. Under "Assets", download the `.tar.gz` bundle that matches your computer. These bundles are the main release files for normal users:
   * **Mac users (M1/M2/M3 chips):** Download `animasola-darwin-arm64.tar.gz`
   * **Mac users (Older Intel chips):** Download `animasola-darwin-amd64.tar.gz`
   * **Linux users:** Download `animasola-linux-amd64.tar.gz`

If you notice any extra files in a release, you can ignore them unless the release notes specifically tell you otherwise. For normal installs, always choose the `.tar.gz` bundle.

### Step 2: Unpack the Bundle
1. Open your **Terminal** app.
2. Navigate to your Downloads folder by typing this and pressing Enter:
   ```bash
   cd ~/Downloads
   ```
3. Unpack the release bundle:
   ```bash
   tar -xzf animasola-linux-amd64.tar.gz
   ```
4. Enter the unpacked folder:
   ```bash
   cd animasola-linux-amd64
   ```

### Step 3: Verify the File is Safe (Optional but Recommended)
Animasola is 100% open-source, which means anyone can read the code to guarantee it is completely safe. But what if you aren't a programmer?

You can use the industry-standard independent security tool **[VirusTotal](https://www.virustotal.com/)**:
1. Go to **VirusTotal.com** in your web browser.
2. Drag and drop the downloaded `.tar.gz` file onto the website.
3. VirusTotal will analyze the file using over 70 different antivirus engines (from Microsoft, Google, BitDefender, etc.).
4. If it comes back clean (0 flags), you have 100% independent proof that the file is safe to open!

**Advanced Users:** We also provide a `checksums.txt` file on the Releases page. You can run `sha256sum animasola-linux-amd64.tar.gz` in your terminal to mathematically guarantee the file wasn't tampered with during the download.

### Step 4: "Install" It So You Can Use It Anywhere
Right now, you can only run the app if you are sitting inside the unpacked folder. That is annoying! 

We want you to be able to open a terminal *anywhere* and just type `animasola` to launch it. The installer keeps everything inside your own home directory so installs, updates, and uninstalls all stay clean and do not need `sudo`.

1. Copy and paste this command and press Enter:
   ```bash
   ./install.sh ~/Downloads/animasola-linux-amd64.tar.gz
   ```

*(What did we just do? The installer put the app in `~/.local/share/animasola/current` and created a launcher at `~/.local/bin/animasola`, keeping the bundled private network runtime, including Tor, alongside it.)*

If you want the shortest possible install path, this also works:
```bash
curl -fsSL https://animasola.org/install.sh | bash
```

### Step 5: Launch It!
You are done! You can now close your terminal, open a brand new one anywhere, and simply type:

```bash
animasola
```

### Step 6: Upgrading Animasola
Animasola includes a built-in updater.

When a new version is released, you will see a banner at the top of the chat: `🚀 UPDATE AVAILABLE`.
To upgrade securely, simply close the app and run:
```bash
animasola update
```
*(This command downloads the latest release and swaps your executable file atomically.)*

If you installed Animasola from one of the normal release bundles, this is the upgrade path you should keep using. If you tried very early alpha builds, Animasola may need to replace older install layouts with the newer bundled layout during upgrade. That is expected while the release line settles down.

### Step 7: Diagnose Problems
If startup fails, run:
```bash
animasola doctor
```
This prints your version, config directory, profile count, and whether Animasola can find its bundled Tor runtime.

### Step 8: Uninstalling Animasola
To remove the installed app files:
```bash
animasola uninstall
```
This removes the app, local profiles, database, and config so the machine is clean again.

If you are coming from a much older alpha build, Animasola may decide that your old local database is not compatible anymore. In that case, it will archive/reset the old database instead of crashing. That keeps the app usable, but it can mean old local chat history does not carry forward from broken alpha-era data.

---

## 🔐 What Happens When I Open It?

Because Animasola is built for privacy, it works a little differently than normal apps like Discord or Slack.

1. **The Warning Screen:** The first time you open it, Animasola will explain that Tor is included for private routing and ask for your consent before starting the private network runtime.
2. **Who Are You?:** Next, it will ask you to create a Profile. Type any name you want (like "MyLaptop"). 
3. **The Magic:** When you hit Enter, the app generates a highly complex mathematical "Cryptographic Key" for you. **This is your permanent identity.** There are no emails, no passwords, and no servers. You are the only person in the universe who owns this mathematical key.
4. **Bootstrapping:** You will see a loading screen while Animasola starts its private network connection. This usually takes around 15 seconds.

## 💬 Inside the App

Once you are in, you will see the Terminal User Interface! Use your keyboard to navigate:

- **Navigation:** Press `j` and `k` (or `Up/Down` arrows) to scroll through your pinned and saved rooms. Press `Enter` to open a room.
- **Search Public Rooms:** Press `s` on your keyboard to enter Search mode. Type any word to filter the list of public rooms discovered over the network. Use `tab` or arrows to select one and press `Enter` to join. Press `Esc` to cancel.
- **Join by ID:** Press `i` to enter a specific Room ID if a friend gave you one privately out-of-band.
- **Create Rooms:** Press `c` to create a new room. If you provide a password, it creates a "Private Room" and gives you a bizarre string of letters and numbers (like `ID: f47ac10b...`). Only people who you give this exact ID to can ever see or join the chat. It is mathematically hidden from the rest of the world!
- **Offline Reading:** The app saves your chats locally in a tiny, compressed file on your computer. You can open `animasola` while on an airplane with no Wi-Fi, and you will still be able to read all your ancient chat history!

---

## 🤖 Built for AI Agents

_(Technical Note for Developers)_

Animasola's entire internal architecture (UI models, SQLite tables, P2P network payloads) is modeled symmetrically using strict JSON contracts and a flat semantic grammar. 

This allowing autonomous AI agents to natively read your local database state, understand the UI structures, and interact with the network directly alongside human users!
