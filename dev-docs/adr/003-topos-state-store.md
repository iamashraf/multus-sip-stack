# ADR 003: Kamailio `topos` state store

Status: accepted, implemented. The user chose `topos_htable` after reviewing the options below.

## Context

[ADR 001](001-necessity-review.md)'s necessity review found, with evidence (not just reasoning), that pod-network IPs already leak into SIP headers the SBC sees today:

- `Record-Route: <sip:10.244.1.31;...>` — Kamailio's own `eth0` pod IP, doubled into a second Record-Route because `force_send_socket()` makes the inbound/outbound socket differ, which the `rr` module reacts to by default.
- `Contact: <sip:9199@10.244.3.35:5060;...>` — FreeSWITCH's pod IP. This is independent of [ADR 002](002-remove-rtpengine.md)'s media-path change, since FreeSWITCH's `sip-ip` stays on `eth0` either way.

Kamailio's `topos` module fixes this: it rewrites Record-Route/Via/Contact on the way out so the SBC only ever sees Kamailio's own address, and decodes them back on the way in using a stored mapping. That mapping needs somewhere to live across Kamailio A/B and across restarts.

## Options

| | Database (e.g. MySQL/Postgres/SQLite) | Redis | `topos_htable` (in-memory, no external store) |
|---|---|---|---|
| What it is | `topos`'s original, most-documented storage backend | A first-class, documented `topos` `storage` option (`modparam("topos", "storage", "redis")`) - corrected below | A separate module (`topos_htable`, added Kamailio 6.0, Dec 2024) providing the in-process-hash-table backend `topos`'s `storage` param also accepts |
| New infrastructure | Yes — a DB instance (or SQLite file per pod) | Yes — a Redis instance | No |
| Survives a Kamailio restart | Yes (external store) | Yes (external store) | No — restart loses in-flight dialogs' mappings |
| Shared between Kamailio A/B | Yes, if both point at the same DB | Yes, if both point at the same Redis | No — each instance's table is local to itself |
| Operational cost | A DB to run, back up, and scope access to | A Redis instance to run; usually simpler to operate than a relational DB, but still a new moving part | None beyond Kamailio itself |
| Verification status | Standard, documented `topos` usage | Documented as a `storage` value, but not exercised in this project | **Implemented and live-verified** (see below) |

## Decision: `topos_htable`

Chosen over the database option this ADR originally recommended, after the user reviewed the tradeoffs. No new infrastructure, and the cost (mappings don't survive a Kamailio restart or share across Kamailio A/B) was accepted as acceptable for this project's scale.

**Correction from the first draft of this ADR**: it claimed Redis "isn't a confirmed `topos` backend." That was wrong - the installed Kamailio 6.0.8's own `topos` module documentation lists `storage` as accepting `db`, `redis`, or `htable` directly, with a worked `modparam("topos", "storage", "redis")` example. Redis was a legitimate option; `topos_htable` was chosen for its no-new-infrastructure property, not because Redis was unsupported.

## Implementation

`config/kamailio/kamailio.cfg.tmpl`: `loadmodule "htable.so"`, `"topos.so"`, `"topos_htable.so"` (in that order - `topos_htable` binds to both as storage/shm engines and needs them loaded first); `modparam("topos", "storage", "htable")`; `modparam("topos_htable", "topos_htable_dialog", ...)` and `"topos_htable_transaction"` with `dmqreplicate=0` (no `dmq` module loaded - mappings stay local to each instance, the accepted tradeoff above). No route-script function calls needed beyond the `record_route()` already present - `topos` rewrites headers via its own callbacks automatically.

Required switching Kamailio's base image from Debian bookworm's own package (5.6.x, predates `topos_htable`) to Kamailio's own `kamailio60` apt repo (`config/kamailio/Dockerfile`) - confirmed live that this repo ships a native arm64 build (`kamailio_6.0.8+bpo12_arm64`), so no emulation workaround was needed here (unlike FreeSWITCH's amd64-only base image).

## Verification

Live, via `test/sipp/call-recording-check.sh` (also see `test/sipp/recordings/`): `check-addresses.sh`'s full-header scan now passes clean - the `Record-Route`/`Contact` pod-IP leak [ADR 001](001-necessity-review.md) found by static analysis, and re-confirmed live before this fix, no longer appears. A full call (INVITE, hold re-INVITE, unhold re-INVITE, BYE) completed correctly with `topos` active, confirming it doesn't interfere with signaling or with the dual-homed `force_send_socket()` routing. Not re-verified: behavior across a Kamailio restart mid-call (expected to lose the mapping, per the accepted tradeoff, but not exercised).
