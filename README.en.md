# Color Personality Test · Local Key Edition (Base64 Branch)

[简体中文](README.md) | English

A ready-to-use color personality test: 30 situational questions, about 3–5 minutes, measuring your personality across four dimensions — Red / Yellow / Blue / Green — with a dominant color, four-dimension scores, and a detailed interpretation (personality reading, strengths, areas to improve, and teamwork & relationship advice).

**The entire tool is a single HTML file**: open it in a browser and take the test — no installation, no server, no internet connection required.

> This is the **HashCode branch**, which differs from the main branch ("LAN Team Edition"): it is a fully local testing tool. Once a test taker finishes, the result is encoded into a **report key** — no result data needs to be stored at all. Keep only the key, and it can be pasted into any client to parse and restore the exact same report.

---

## Differences from the Main Branch

| | Main branch (LAN Team Edition) | This branch (Local Key Edition) |
|---|---|---|
| How it runs | Go server program with embedded web page | Single HTML file, opens directly in a browser |
| Result storage | Records centralized in the host's `records.json` | **Results are encoded into the report key; no result data is stored** |
| Retrieving a report | 6-digit report password / admin console | Paste a `CSX1-` key on any device to restore the report |
| Network requirement | Requires LAN or internet access | Core features fully offline (only optional UI translation needs network) |
| Admin console | Yes | None (an "initiator key" opens the local record center) |

## Core Mechanism: The Report Key

This branch follows a "**key is the data**" design:

```text
Test finished ──► Result packed & encoded into a report key (CSX1-…) ──► Keep only the key, store nothing else
                                                                      │
Open the tool on ANY client ◄── Paste the key on the home page ◄──────┘
        Fully restored: name, test time, all 30 answers, four-dimension scores, and the whole report
```

- After submission, the result page automatically generates a **report key** prefixed with `CSX1-`, encoding: the name, test time, all 30 answers, and the four-dimension scores;
- **Neither a server nor the local device needs to store the result** — the key itself is the complete carrier of the report;
- Anyone holding the key can paste it into "View report with key" on the home page and restore the exact same report offline on **any client** (phone / desktop / any modern browser) — no network, no account;
- Both key generation and parsing happen entirely in the local browser (URL-safe Base64), with zero network requests throughout.

> **Privacy note**: the key is equivalent to the report itself (Base64 is encoding, not strong encryption). Only share the key with people you allow to see the result — leaking the key means leaking the result.

### Two Kinds of Keys

| Key | Form | Purpose |
|---|---|---|
| **Report key** | Starts with `CSX1-`, generated automatically after a test | The carrier of a single report: share it for others to view, or keep it for your own records — paste it anytime to restore |
| **Initiator key** | 8-digit number (default `12345678`) | Enter it on the home page to open the **local record center** and browse all records saved on this device |

The initiator key is merely the entrance to the local record center. It is defined by the `MASTER_KEY` constant in the source and can be changed freely.

## Features

- **30-question quick test** (about 3–5 minutes): four-dimension scores and dominant-color determination, with clear notices for ties (two-color dominance / three-color tie / fully balanced)
- **Detailed report**: dominant color card, four-dimension chart (with levels such as "significant dominance / clear tendency"), personality reading, strengths and areas to improve, teamwork & relationship advice, and secondary color
- **Report key**: one-click copy, ready to share; reports parsed from a key can also be saved to the local device with one click
- **Local record center** (via the initiator key): view, delete single records, clear all; keeps the most recent 300 records automatically
- **Multiple exports**: report as a long PNG image or TXT archive; the record center supports exporting all records as TXT, and `.json` backup export / import & merge (cross-device migration, deduplicated by record ID)
- **Zero-dependency offline operation**: single file, no build, no installation; edits to the HTML take effect immediately, no compilation needed
- **Desktop shortcuts**: `←` / `→` to navigate questions, `1–4` or `A–D` to answer quickly
- **WeChat support**: detects the WeChat in-app browser and prompts "Open in browser" to enable downloads
- **UI multilingual support (optional)**: a translate.js online-translation widget is attached at the end of the page; to go fully offline, simply remove the two corresponding `<script>` blocks

## Quick Start

1. Download (or clone) `色彩性格测试.html` from this branch;
2. Open it with any modern browser — double-click on desktop, or transfer the file to your phone and open it there;
3. Enter a name (optional) → start the test → after submitting, **copy the report key immediately** and keep it safe.

You can also host the file on any static hosting (GitHub Pages, Nginx, object-storage static websites, etc.) and share the link with test takers — they can take the test right in the browser.

> Tip: the "Copy key" button relies on the clipboard API and works best under HTTPS or localhost (other environments automatically fall back to a compatible method).

## Workflow

**Test takers**

1. Open the page → enter a name → answer all 30 questions;
2. On the result page, click "Copy report key" and send the key to yourself or the organizer; optionally export a PNG / TXT archive.

**Report viewers / organizers**

1. Open the same HTML file on any device;
2. Paste the key into "View report with key" on the home page → the full report appears immediately;
3. To keep records in one place, click "Save to this device", then use the initiator key to open the record center and review / export everything.

## Scoring Rules

Each question has four options mapped to the four colors; the mapping direction flips between the first and second halves:

| Color | Questions 1–15 | Questions 16–30 |
|---|---|---|
| Red (Expressive & Enthusiastic) | A | D |
| Yellow (Goal-driven) | C | B |
| Blue (Rigorous & Perfectionist) | B | C |
| Green (Calm & Steady) | D | A |

Each dimension is scored out of 30: ≥12 significant dominance, ≥9 clear tendency, ≥5 moderate, ≥1 relatively weak.

## Data & Privacy

- **Zero uploads**: apart from the optional UI translation service, the tool makes no network requests at all — test data never leaves the device;
- **Local records**: stored only in the current browser's `localStorage` (key `csx_records_v1`, up to 300 records); if local storage is disabled (e.g., private mode), the tool falls back to in-memory storage for the current session;
- **Leave no trace**: keep only the key after the test and delete the local record in the record center — then the result exists solely in the key you hold;
- **Switching devices**: paste the key to restore a single report, or migrate all records with a `.json` backup file;
- **Clearing browser data deletes local records**, but saved keys / PNG / TXT / JSON files are unaffected — a key can restore its report at any time.

## Customization

Open `色彩性格测试.html` and edit as needed:

| What to change | Where |
|---|---|
| Questions and options | `QUESTIONS` array (4 options per question) |
| Color names / theme colors | `DIMS` object |
| Readings / strengths / improvements / advice | `PROFILE` object |
| Initiator key | `MASTER_KEY` constant |
| Key prefix | `KEY_PREFIX` constant |
| Remove online translation (fully offline) | Delete the two `<script>` blocks referencing translate.js at the end of the file |

## FAQ

- **Lost the key — can I recover it?** The key is never stored on any server. Reopen the report from the local record center (via the initiator key) and copy the key again, or find it in exported PNG / TXT / JSON files; if the records were cleared and no backup exists, it cannot be recovered — make a habit of saving the key right after each test.
- **Is it safe to give the key to others?** The key *is* the report (encoding, not encryption); just share it only with people allowed to see the result.
- **"Invalid key" error?** Make sure the key starts with `CSX1-` and was copied in full; spaces and line breaks inside the key are ignored automatically.
- **Can't download images / text inside WeChat?** Tap "···" in the top-right corner → "Open in browser", then download; TXT content can also be copied with one tap.
- **Want it fully offline?** Remove the two scripts referencing translate.js at the end of the file — translation is an optional UI feature; the test and the key mechanism never touch the network.

## License

This project is open-sourced under the [GPL-3.0 License](../../blob/HashCode/LICENSE). Feel free to adapt the questions and copy to your needs.
