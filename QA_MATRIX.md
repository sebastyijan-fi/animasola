# Animasola QA Matrix

This is the working release test matrix for Animasola.

Goal:
- one clear list of what must work
- one current status per flow
- one failure ledger we can work through without guessing

Status values:
- `PASS`
- `FAIL`
- `PARTIAL`
- `UNVERIFIED`

## 1. Install and lifecycle

| Area | Test case | Expected result | Status | Notes |
|---|---|---|---|---|
| Install | Fresh install with `curl -fsSL https://animasola.org/install.sh | bash` | App installs user-local without sudo | `PASS` | Verified on local machine and VPS |
| Install | Installed launcher available on PATH | `animasola` runs directly | `PASS` | Verified locally; VPS path warning is expected for service user shell |
| Uninstall | `animasola uninstall` | App files and local Animasola data removed | `PASS` | Implemented as full removal |
| Update | `animasola update` from prior alpha | Updates to latest signed release | `PASS` | Verified in release pipeline, used repeatedly during this cycle |
| Release integrity | Bundle is signed and install verifies signature | Unverified/tampered bundle is rejected | `PASS` | Signed manifest path implemented |
| Release integrity | Updater verifies signed release manifest | Unverified/tampered update is rejected | `PASS` | Implemented and tested earlier |

## 2. First run

| Area | Test case | Expected result | Status | Notes |
|---|---|---|---|---|
| First run | Launch app after clean install | App reaches setup cleanly | `PASS` | Verified on VPS |
| First run | Create new profile name | Profile is created and registered | `PASS` | Verified on VPS with `codexprobe` |
| First run | Name already taken | User gets plain retryable error | `PASS` | Covered earlier in registry-gated flow |
| First run UX | Setup language is plain and non-technical | No developer/security jargon in main flow | `PARTIAL` | Improved; should still get one more polish pass |
| First run UX | No overlapping loading states | User sees one clear loading state at a time | `PARTIAL` | Home stale banner fixed; full state cleanup still incomplete |

## 3. Bootstrap and startup

| Area | Test case | Expected result | Status | Notes |
|---|---|---|---|---|
| Tor startup | Bundled Tor starts from release bundle | App boots without external Tor install | `PASS` | Verified in release flow |
| Tor startup | Startup reaches Home after Tor bootstraps | User lands in Home | `PASS` | Verified on VPS |
| Bootstrap peers | Embedded bootstrap peers used automatically | User does not configure peers manually | `PASS` | Current release path |

## 4. Identity and registration

| Area | Test case | Expected result | Status | Notes |
|---|---|---|---|---|
| Global profile name | Local profile name is the global public name | Registered profile name is authoritative | `PASS` | Registry-gated |
| Registration auth | Profile registration requires signed challenge | Raw spoof requests fail | `PASS` | Fixed and tested |
| Registration limits | Repeated profile farming from one source is throttled | Abuse is slowed materially | `PASS` | Active cap + cooldown + window limits |
| Profile deletion | Deleting profile frees active slot | User can create replacement profile later | `PASS` | Implemented and tested |

## 5. Public room search and discovery

| Area | Test case | Expected result | Status | Notes |
|---|---|---|---|---|
| Public room create | Create public room from valid registered profile | Public room is registered | `PASS` | Registry path working |
| Public room search | Fresh user searches for existing public room | Room appears in search | `PASS` | Fixed in `v0.2.21-alpha` by using registry-backed search |
| Public room discovery | Gossip-forwarded public room announce is accepted | Downstream nodes can index forwarded announce | `PASS` | Fixed and covered by new forwarded discovery test |
| Public room search UX | Search should not depend on creator being online | Registry returns stable public room results | `PASS` | Now uses registry |
| Public room limits | More than 3 public rooms per profile are blocked | 4th room fails | `PASS` | Registry enforced |

## 6. Public room usage

| Area | Test case | Expected result | Status | Notes |
|---|---|---|---|---|
| Public room join | Join discovered public room from search | Opens room and stays there | `UNVERIFIED` | Needs fresh repro after `v0.2.21-alpha` |
| Public room messaging | Send message in discovered public room | Message sends, room stays open | `FAIL` | User reported being kicked out after writing |
| Public room persistence | Joined public room remains in Home after reopening app | Room is visible and reopenable | `UNVERIFIED` | Not yet rechecked after search fix |
| Public room cross-user messaging | Second user sees messages in public room | Real cross-user chat works | `UNVERIFIED` | Needs explicit dogfood pass |

## 7. Private room usage

| Area | Test case | Expected result | Status | Notes |
|---|---|---|---|---|
| Private room create | Create private room | Room opens and is local/invite-based | `UNVERIFIED` | Needs current release pass |
| Private room join | Join private room by ID + password | Opens room | `UNVERIFIED` | Needs current release pass |
| Private room messaging | Two peers exchange private messages | Messages flow end to end | `UNVERIFIED` | Needs current release pass |
| Private room visibility | Private room does not appear in public search | Remains private | `PASS` | Established earlier |

## 8. Room lifecycle and UX

| Area | Test case | Expected result | Status | Notes |
|---|---|---|---|---|
| Room create UX | Creating room uses plain language | No technical wording | `PASS` | Updated recently |
| Room join UX | Joining room uses plain language | No technical wording | `PASS` | Updated recently |
| Room info UX | Info panel uses user language | Simple room metadata and actions | `PARTIAL` | Better, but still could be simplified further |
| Home state | Search/create/join/info/delete do not overlap visually | One panel state at a time | `PARTIAL` | Derived panel state added; full topology cleanup still pending |

## 9. Messaging integrity and abuse controls

| Area | Test case | Expected result | Status | Notes |
|---|---|---|---|---|
| Forged author | Fake author payload is rejected | No spoofed chat authors | `PASS` | Covered by attack lab |
| Username spoof | Different peer cannot speak as another registered name | Invalid name binding rejected | `PASS` | Fixed and tested |
| Username rewrite | Old messages keep original author snapshot | History does not rewrite | `PASS` | Fixed and tested |
| Single-sender flood | Rapid flood from one sender is bounded | Limited stored messages | `PASS` | Attack lab |
| Multi-sender flood | Coordinated room flood is bounded | Aggregate room cap holds | `PASS` | Attack lab |
| Metadata churn | Rapid room metadata changes are bounded | Cooldown blocks churn | `PASS` | Attack lab |

## 10. Current failure ledger

### Open failures

1. `Public room messaging can kick the user out of the room`
- Reported after public room search started working.
- Symptom: user enters discovered public room, writes a message, then gets kicked back out.
- Status: `OPEN`
- Suspect area:
  - room open / subscribe flow
  - room message send path
  - room error propagation back to app/home

2. `Attack lab no longer reflects the live registry model for multi-profile scenarios`
- Several hostile scenarios now fail at profile registration before they reach the message/discovery behavior they were originally written to test.
- This is because the live control plane now enforces:
  - per-source registration attempt limits
  - active profile caps
  - issuance cooldowns
- Status: `OPEN`
- This is a test harness gap, not a product security regression.

### Recently fixed

1. `Fresh nodes could not discover public rooms`
- Cause: public search relied on gossip timing and forwarded gossip validation was wrong.
- Fix:
  - forwarded discovery now validates against the signed creator
  - user-facing search now uses registry-backed public room search

2. `Home showed stale generic identity-derivation status`
- Cause: overlapping Home booleans and generic processing copy.
- Fix: derived panel state + action-specific busy text.

3. `Selecting a room from search opened an ephemeral result`
- Cause: registry-backed search results were not persisted locally before opening.
- Fix: search selection now materializes the room locally before open.

## 11. Next execution order

1. Reproduce the public-room kick-out bug on `v0.2.21-alpha`
2. Trace room open/send/error path
3. Mark public room join/send as `PASS` or keep as `FAIL`
4. Run private-room creation/join/messaging tests
5. Run one full clean dogfood checklist from install to uninstall
