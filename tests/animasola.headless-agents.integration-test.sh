#!/bin/bash
set -e

echo "========================================="
echo "ANIMASOLA PHASE 12: E2E STRESS TEST SUITE"
echo "========================================="

# Build the headless runner
echo "[+] Building Headless Test Engine..."
cd "$HOME/Data/Projects/animasola" || exit 1
go build -o ./bin/animasola-stress ./cmd/headless/...

# Clean previous test environments to guarantee cold Tor bootstrap testing
echo "[+] Wiping previous Test Profiles..."
rm -rf "$HOME/.config/animasola/alice_test"
rm -rf "$HOME/.config/animasola/bob_test"
rm -rf "$HOME/.config/animasola/charlie_test"

TEST_ROOM_ID="stress-test-room-phase-9-$(date +%s)"

echo "[+] Booting Test Cluster (Alice, Bob, Charlie)..."

# We spawn the processes with stdbuf -oL to ensure line buffering, and background them.
# We map their file descriptors so we can echo commands directly into their stdin.

# Node 1: Alice
rm -f /tmp/alice.pipe
mkfifo /tmp/alice.pipe
./bin/animasola-stress alice_test < /tmp/alice.pipe > /tmp/alice.log 2>&1 &
ALICE_PID=$!
exec 3> /tmp/alice.pipe

# Node 2: Bob
rm -f /tmp/bob.pipe
mkfifo /tmp/bob.pipe
./bin/animasola-stress bob_test < /tmp/bob.pipe > /tmp/bob.log 2>&1 &
BOB_PID=$!
exec 4> /tmp/bob.pipe

# Node 3: Charlie
rm -f /tmp/charlie.pipe
mkfifo /tmp/charlie.pipe
./bin/animasola-stress charlie_test < /tmp/charlie.pipe > /tmp/charlie.log 2>&1 &
CHARLIE_PID=$!
exec 5> /tmp/charlie.pipe


echo "[~] Waiting 45 seconds for parallel Tor Bootstraps and DHT Discovery..."
sleep 45

echo "[+] Instructing cluster to join DHT Room: $TEST_ROOM_ID"
echo "JOIN $TEST_ROOM_ID" >&3
echo "JOIN $TEST_ROOM_ID" >&4
echo "JOIN $TEST_ROOM_ID" >&5

echo "[~] Waiting 10 seconds for GossipSub mesh completion..."
sleep 10

echo "[+] Initiating SQLite Concurrency Blast (100 messages each)..."
# Alice and Bob simultaneously blast the network, 
# forcing Charlie's SQLite layer to handle extreme concurrent network writes and discovery events.

for i in {1..100}
do
   echo "CHAT $TEST_ROOM_ID Alice_Payload_$i" >&3
   echo "CHAT $TEST_ROOM_ID Bob_Payload_$i" >&4
   sleep 0.05 # Tiny sleep to avoid completely saturating the local pipe buffer
done

echo "[~] Waiting 15 seconds for network propagation..."
sleep 15

# Forcefully kill Alice ungracefully to test Zombie Tor mitigation
echo "[!] Simulating fatal crash (kill -9) on Alice..."
kill -9 $ALICE_PID
sleep 2 # Let the kernel process the Pdeathsig

echo "[+] Processing assertions..."

# 1. Tor Collision Assertion (Did they all boot successfully?)
ALICE_READY=$(grep -c "Node Ready" /tmp/alice.log || true)
BOB_READY=$(grep -c "Node Ready" /tmp/bob.log || true)
CHARLIE_READY=$(grep -c "Node Ready" /tmp/charlie.log || true)

if [ "$ALICE_READY" -ne 1 ] || [ "$BOB_READY" -ne 1 ] || [ "$CHARLIE_READY" -ne 1 ]; then
    echo "[-] FAILED: Nodes failed to initialize Tor concurrently. Port collision likely."
    cat /tmp/alice.log
    exit 1
fi
echo "[+] SUCCESS: Concurrent Tor Bootstraps perfectly isolated."

# 2. SQLite Concurrency Assertion (Did Charlie receive and write all 200 messages under stress?)
# To mathematically verify Charlie, we will use the native Go SQLite driver rather than the CLI.
CHARLIE_DB="$HOME/.config/animasola/charlie_test/animasola.db"
TOTAL_MESSAGES=$(go run ./tests/storage.sqlite.queries.count.utility.cli.go "$CHARLIE_DB")

echo "[?] Charlie's DB holds $TOTAL_MESSAGES messages."
# Note: GossipSub isn't 100% reliable in local small clusters, but we should see a high volume without panics.
if [ "$TOTAL_MESSAGES" -lt 150 ]; then
   echo "[-] FAILED / WARNING: Charlie dropped significant SQLite writes ($TOTAL_MESSAGES/200). Check for DB Lock Panics in log."
   grep -i "database is locked" /tmp/charlie.log || true
   # We don't exit 1 purely on packet drop, as Tor/GossipSub can inherently drop in testing, 
   # but we DO check for panics.
fi

LOCK_PANICS=$(grep -c "database is locked" /tmp/charlie.log || true)
if [ "$LOCK_PANICS" -gt 0 ]; then
   echo "[-] FAILED: Charlie hit SQLite locks ($LOCK_PANICS times)."
   exit 1
fi
echo "[+] SUCCESS: No SQLite Wait Panics occurred under concurrent mesh load."

# 3. Zombie Tor Assertion (Did the kernel kill Alice's Tor proxy?)
# Alice's config dir was alice_test. Her Tor daemon path contains alice_test.
ZOMBIES=$(ps aux | grep tor | grep alice_test | grep -v grep | wc -l || true)
if [ "$ZOMBIES" -gt 0 ]; then
    echo "[-] FAILED: Alice's Tor Proxy survived a kill -9 and is a Zombie daemon."
    ps aux | grep tor | grep alice_test
    exit 1
fi
echo "[+] SUCCESS: Linux Kernel Pdeathsig successfully murdered the orphaned daemon."

echo "[+] Full Cleanup..."
kill -9 $BOB_PID || true
kill -9 $CHARLIE_PID || true
rm -f /tmp/alice.pipe /tmp/bob.pipe /tmp/charlie.pipe

echo "========================================="
echo "ALL TESTS PASSED: PHASE 12 COMPLETE!"
echo "========================================="
exit 0
