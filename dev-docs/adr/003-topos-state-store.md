# ADR 003: Kamailio `topos` state store

Status: proposed, awaiting decision. Not implemented — `topos` is not yet wired into `config/kamailio/kamailio.cfg.tmpl`, per the instruction to ask before adding its state store.

## Context

[ADR 001](001-necessity-review.md)'s necessity review found, with evidence (not just reasoning), that pod-network IPs already leak into SIP headers the SBC sees today:

- `Record-Route: <sip:10.244.1.31;...>` — Kamailio's own `eth0` pod IP, doubled into a second Record-Route because `force_send_socket()` makes the inbound/outbound socket differ, which the `rr` module reacts to by default.
- `Contact: <sip:9199@10.244.3.35:5060;...>` — FreeSWITCH's pod IP. This is independent of [ADR 002](002-remove-rtpengine.md)'s media-path change, since FreeSWITCH's `sip-ip` stays on `eth0` either way.

Kamailio's `topos` module fixes this: it rewrites Record-Route/Via/Contact on the way out so the SBC only ever sees Kamailio's own address, and decodes them back on the way in using a stored mapping. That mapping needs somewhere to live across Kamailio A/B and across restarts.

## Options

| | Database (e.g. MySQL/Postgres/SQLite) | Redis | `topos_htable` (in-memory, no external store) |
|---|---|---|---|
| What it is | `topos`'s original, most-documented storage backend | Not a native `topos` backend as far as could be confirmed here — would likely mean a custom/`ndb_redis`-based integration, unverified | A separate, lighter Kamailio module storing the mapping in an in-process hash table |
| New infrastructure | Yes — a DB instance (or SQLite file per pod) | Yes — a Redis instance | No |
| Survives a Kamailio restart | Yes (external store) | Yes (external store) | No — restart loses in-flight dialogs' mappings |
| Shared between Kamailio A/B | Yes, if both point at the same DB | Yes, if both point at the same Redis | No — each instance's table is local to itself |
| Operational cost | A DB to run, back up, and scope access to | A Redis instance to run; usually simpler to operate than a relational DB, but still a new moving part | None beyond Kamailio itself |
| Verification status | Standard, documented `topos` usage | **Unverified** — needs confirming against the pinned Kamailio version whether `topos` supports a Redis-backed storage API at all, or whether this would mean non-standard glue code | Exists as a module in modern Kamailio (5.3+); exact version support not yet verified against this project's (still unpinned) Kamailio version |

## Recommendation

Database, with a strong caveat: Kamailio's version isn't pinned yet in `dev-docs/versions.md` ("Kamailio... image tags are pinned in Phases 2-4"), so the exact backend options available should be re-confirmed against whichever version gets pinned before this is implemented. If dialog mappings only need to survive within one call (not across a Kamailio restart mid-call), `topos_htable` is worth considering instead — it adds no new infrastructure, at the cost of losing in-flight mappings on restart, which matters most during the small window a call is active when a pod restarts.

Redis is listed because it was asked for directly, but it isn't a confirmed, documented `topos` backend the way a SQL database is — picking it would need a verification pass against the pinned Kamailio version first, not an assumption that it's a drop-in option.

## Decision needed

Which backend to implement: database (and which engine), `topos_htable`, or something else. Not decided here — this ADR proposes the trade-offs per the instruction to ask before adding the state store.
