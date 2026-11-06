# ADR 003: Kamailio `topos` state store

Status: accepted, implemented. The user chose `topos_htable` after reviewing the options below.

## Context

[ADR 001](001-necessity-review.md)'s necessity review found, with evidence (not just reasoning), that pod-network IPs already leak into SIP headers the SBC sees today:

- `Record-Route: <sip:10.244.1.31;...>` is Kamailio's own `eth0` pod IP, doubled into a second `Record-Route` because `force_send_socket()` makes the inbound and outbound sockets differ. The `rr` module reacts to that difference by default.
- `Contact: <sip:9199@10.244.3.35:5060;...>` is FreeSWITCH's pod IP. This leak is independent of [ADR 002](002-remove-rtpengine.md)'s media-path change, since FreeSWITCH's `sip-ip` stays on `eth0` either way.

Kamailio's `topos` module fixes both leaks. It rewrites `Record-Route`, `Via`, and `Contact` on the way out, so the SBC sees only Kamailio's own address, and decodes them back on the way in using a stored mapping. That mapping needs somewhere to live across `kamailio-a` and `kamailio-b`, and across restarts.

## Options

| | Database (for example MySQL, Postgres, or SQLite) | Redis | `topos_htable` (in-memory, no external store) |
|---|---|---|---|
| What it is | `topos`'s original, most-documented storage backend | A first-class, documented `topos` `storage` option (`modparam("topos", "storage", "redis")`). See the correction below. | A separate module, `topos_htable`, added in Kamailio 6.0 (December 2024), that provides the in-process hash-table backend that `topos`'s `storage` param also accepts |
| New infrastructure | Yes, a DB instance (or SQLite file per pod) | Yes, a Redis instance | No |
| Survives a Kamailio restart | Yes (external store) | Yes (external store) | No, a restart loses in-flight dialogs' mappings |
| Shared between `kamailio-a` and `kamailio-b` | Yes, if both point at the same DB | Yes, if both point at the same Redis | No, each instance's table is local to itself |
| Operational cost | A DB to run, back up, and scope access to | A Redis instance to run. Usually simpler to operate than a relational DB, but still a new moving part | None beyond Kamailio itself |
| Verification status | Standard, documented `topos` usage | Documented as a `storage` value, but not exercised in this project | **Implemented and live-verified** (see below) |

## Decision: `topos_htable`

The user chose `topos_htable` over the database option that this ADR originally recommended, after reviewing the tradeoffs. It needs no new infrastructure. The cost, that mappings don't survive a Kamailio restart or share across `kamailio-a` and `kamailio-b`, was accepted for this project's scale.

**Correction from the first draft of this ADR**: the first draft claimed Redis "isn't a confirmed `topos` backend." That was wrong. The installed Kamailio 6.0.8's own `topos` module documentation lists `storage` as accepting `db`, `redis`, or `htable` directly, with a worked `modparam("topos", "storage", "redis")` example. Redis was a legitimate option. The user chose `topos_htable` because it needs no new infrastructure, not because Redis was unsupported.

## Implementation

`config/kamailio/kamailio.cfg.tmpl` loads `"htable.so"`, `"topos.so"`, and `"topos_htable.so"`, in that order, because `topos_htable` binds to both as storage and shm engines and needs them loaded first. It sets `modparam("topos", "storage", "htable")`, then `modparam("topos_htable", "topos_htable_dialog", ...)` and `"topos_htable_transaction"` with `dmqreplicate=0`. No `dmq` module is loaded, so mappings stay local to each instance, the accepted tradeoff above. The config needs no route-script function calls beyond the `record_route()` already present. `topos` rewrites headers through its own callbacks automatically.

This implementation required switching Kamailio's base image from Debian bookworm's own package (5.6.x, which predates `topos_htable`) to Kamailio's own `kamailio60` apt repo (`config/kamailio/Dockerfile`). Live testing confirmed that this repo ships a native arm64 build (`kamailio_6.0.8+bpo12_arm64`), so no emulation workaround was needed here, unlike FreeSWITCH's `amd64`-only base image.

## Verification

Verified live through `test/sipp/call-recording-check.sh` (see `test/sipp/recordings/` for the recordings). `check-addresses.sh`'s full-header scan now passes clean. The `Record-Route` and `Contact` pod-IP leak that [ADR 001](001-necessity-review.md) found by static analysis, and that was re-confirmed live before this fix, no longer appears. A full call (INVITE, hold re-INVITE, unhold re-INVITE, BYE) completed correctly with `topos` active. This confirms that `topos` doesn't interfere with signaling or with the dual-homed `force_send_socket()` routing. Not re-verified: behavior across a Kamailio restart mid-call. That's expected to lose the mapping, per the accepted tradeoff, but hasn't been exercised.
