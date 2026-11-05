# ADR 002: Remove rtpengine

Status: accepted.

Supersedes the "Media path: rtpengine bridging `net1`/`eth0` vs. routable pod IPs (Calico BGP) with no rtpengine" item previously listed as deferred in `dev-docs/architecture.md`.

## Decision

rtpengine is removed from the design and the implementation. The SBC is the only media relay. Media runs **SBC <-> Kamailio** (SIP only) and **SBC <-> FreeSWITCH `net1`** (RTP, direct).

## Why

rtpengine existed to solve one problem: making media reachable when the far end can't reach FreeSWITCH. That problem doesn't exist here.

1. **The SBC already anchors media.** It terminates carrier RTP and starts a new stream toward us. A second relay inside the cluster duplicated that job — confirmed in [ADR 001](001-necessity-review.md)'s necessity review.
2. **FreeSWITCH can own a reachable address.** With Multus, a FreeSWITCH pod gets a `net1` on the voice network. The IP it puts in SDP is reachable by the SBC directly, and replies leave from the same IP. No NAT, so nothing needs to rewrite SDP.
3. **The port range problem goes away.** The SBC sends RTP straight to the FreeSWITCH pod IP. No Kubernetes Service, no port exposure, no range needed in Kubernetes.
4. **It added cost with no benefit here:** an extra media hop (latency, another place to drop packets), another component to scale/drain/monitor, another failure point (one rtpengine pod dying killed audio on every call routed through it — it ran unreplicated).
5. **It didn't improve resilience.** If a FreeSWITCH pod dies, its calls drop with or without rtpengine.

Note, corrected against the original premise: as actually built in this repo, rtpengine ran plain userspace forwarding — no kernel module, no added Linux capabilities, no privileged container (`config/rtpengine/entrypoint.sh`, `charts/rtpengine/templates/deployment.yaml`, both now removed). The "needs a host kernel module and extra privileges" cost would only have applied if in-kernel offload had been turned on later; it isn't a defect this removal needed to fix, just a premise correction.

## Pre-check (done before any change)

- No transcoding, SRTP/DTLS bridging, WebRTC, or recording logic depended on rtpengine. Every hit for those terms was stock FreeSWITCH default-config boilerplate, unrelated to rtpengine.
- `dispatcher-sync` never synced anything rtpengine-related — nothing to remove there.
- No rtpengine drain logic, PodDisruptionBudget, metrics, dashboards, or `make images` target existed.
- One real test-asset dependency: `local/sbc/scenarios/uac-demo.xml` primed "symmetric RTP learning," a real rtpengine behavior (it won't relay to an address it hasn't received a packet from). That behavior doesn't apply once FreeSWITCH is a direct, non-NATed destination — the scenario's comment was corrected; the harmless warm-up packets were left in place.
- `dev-docs/prerequisites.md`'s SBC/firewall assumptions ("pool sized for peak sessions," "RTP port range -> the pool") generalized cleanly to the FreeSWITCH pool.

## What was removed

- `charts/rtpengine/` and `config/rtpengine/` entirely.
- `rtpengine.pool` values (renamed to `freeswitch.pool` in `charts/voice-network`, since the same NAD now serves FreeSWITCH).
- `RTPENGINE_HOST` env var and `rtpengineHost` value from Kamailio.
- `loadmodule "rtpengine.so"`, its `modparam`, every `rtpengine_manage()` call, and the `onreply_route[MANAGE_REPLY]` hook from `kamailio.cfg.tmpl`. Kamailio is SIP-only now: dispatch, `record_route()`, and the `force_send_socket()` dual-homing logic stay (still needed for SIP between `net1` and `eth0`).
- All rtpengine references from `README.md`, `dev-docs/architecture.md`, `dev-docs/local-lab.md`, `dev-docs/verification.md`, `dev-docs/prerequisites.md`, `dev-docs/versions.md`, `local/Makefile`.
- `test/sipp/media-flow-check.sh` rewritten from a two-leg (SBC->rtpengine, rtpengine->FreeSWITCH) check to a single-leg direct SBC<->FreeSWITCH check.

## What changed

**FreeSWITCH** now owns a `net1` address and anchors media itself:
- Attaches `voice-pool` via the Multus annotation (`k8s.v1.cni.cncf.io/networks: voice-pool`).
- A new `config/freeswitch/entrypoint.sh` (built into a thin wrapper image, `config/freeswitch/Dockerfile`, over the upstream `ghcr.io/patrickbaus/freeswitch-docker:1.11.3`) reads `net1`/`eth0` at startup, fails fast with a clear log line if `net1` is missing, and checks `ip route get $SBC_MEDIA_IP` resolves via `net1` before starting FreeSWITCH.
- Sofia profile (`config/freeswitch/internal.xml`, verified directly against the running config already in this repo rather than external docs): `sip-ip`/`ext-sip-ip` now read `$${sip_ip_v4}` (eth0); `rtp-ip`/`ext-rtp-ip` now read `$${rtp_ip_v4}` (net1) and a plain `__NET1_IP__` substitution for `external_rtp_ip`/`external_sip_ip` in `config/freeswitch/vars.xml.tmpl` (renamed from `vars.xml` since it's now templated the same way `kamailio.cfg.tmpl` is).
- Scheduling renamed from "edge nodes" to **voice nodes** everywhere: the taint is now `voice-node=true:NoSchedule`, the label `voice-role: voice`, and FreeSWITCH carries both the toleration and the matching `nodeSelector` (it previously used an untainted, differently-labeled `media` pool).

**Network attachment** (`charts/voice-network`):
- `freeswitch.pool.rangeStart`/`rangeEnd` replace `rtpengine.pool.*`.
- `charts/voice-network/templates/nad-pool.yaml` now fails `helm template`/`helm install` with a clear message if the pool is smaller than `freeswitch.maxReplicas + maxSurge + poolHeadroom` (new values, documented in `charts/voice-network/values.yaml`). Shortcut: assumes `rangeStart`/`rangeEnd` share one /24; doesn't support a range spanning a subnet boundary.

**Kamailio**: SIP only now. `record_route()` stays. Topology hiding (`topos`) is proposed separately — see [ADR 003](003-topos-state-store.md) — and was not added in this pass, per the instruction to ask before adding its state store. (Added in a later pass - see "topos and the AC5 load test" below.)

**Local lab**: all three kind workers (not two) attach to the `voice` Docker network (`local/voice-net.sh`, `local/kind.yaml`), since FreeSWITCH needs it now too. `local/addons.sh`'s CNI-plugin-copy step now targets all `voice-role=voice` nodes instead of just the former edge pair.

**Tests**:
- `local/check-addresses.sh` now scans the full decoded SIP/SDP message (`tshark -V`) instead of only `sip.msg_body` — closes the header-leak gap [ADR 001](001-necessity-review.md) found (pod IPs were already leaking into `Record-Route`/`Contact`/`Via` before this change, independent of rtpengine).
- `test/sipp/media-flow-check.sh` now captures only from the SBC (one leg, since there's only one hop) and asserts RTP flows both directions directly between the SBC and FreeSWITCH's `net1` address.

## What we give up

- FreeSWITCH now needs voice-VLAN nodes, not just ordinary pod-network nodes — a bigger footprint on the constrained VLAN-attached node pool.
- A larger IP block: the pool now has to cover FreeSWITCH's replica ceiling plus rollout surge plus headroom, not a small fixed relay pool.
- No central point for media-layer stats — rtpengine's `rtpengine-ng` control port was a natural place to pull jitter/loss from across every call; that now has to come from FreeSWITCH's own call variables per pod (already the plan for `make quality` regardless).
- A `topos` state store — not previously needed because rtpengine's own SDP rewriting incidentally also kept FreeSWITCH's address out of Kamailio's visible SIP headers to some degree; it never fully worked (see ADR 001), so this is a pre-existing gap being closed properly, not new cost caused by this change. (Implemented in a later pass via `topos_htable`, no new infrastructure — see ADR 003.)

## Triggers that would bring a media relay back

Re-evaluate (new ADR, don't just flip a flag) if any of these shows up:
- WebRTC or browser clients needing ICE/DTLS-SRTP termination.
- Carriers connecting without an SBC in front of them.
- Media pods scheduled on nodes without the voice VLAN NIC.
- Media forking for recording outside what FreeSWITCH and the SBC can do themselves.

The `charts/rtpengine` chart is deleted, not disabled-by-default — if one of these triggers fires, it needs to be rebuilt (or re-added from version control history) rather than re-enabled from a flag.

## Verification

**Static**: `helm template`/`helm lint` on all 4 remaining charts render clean with `environments/local/values.yaml`, including the pool-size guard (confirmed it both passes at the real range and fails with a clear message when shrunk below the minimum). `grep -ri rtpengine` across the repo returns nothing outside the ADRs and 3 explained exceptions (an ADR filename in a link, a deliberately-preserved historical record in `verification.md`, and the two generated HTML diagrams, not hand-edited here).

**Live** (kind lab recreated with the new `kind.yaml`/topology, `kamailio` and `freeswitch` images rebuilt and kind-loaded, all 4 charts installed):

- **AC3 (direct audio)**: confirmed. A real call through `test/sipp/media-flow-check.sh` showed 249/244 RTP packets flowing both directions directly between the SBC (`192.168.50.5`) and the FreeSWITCH pod that took the call (`192.168.50.128`, its own `net1`) — no relay IP anywhere in the capture. `fs_cli -x "sofia status profile internal"` confirmed `RTP-IP`/`Ext-RTP-IP` = the `net1` address, `SIP-IP`/`Ext-SIP-IP` = the `eth0` address, exactly as designed.
- **AC4 (address/interface/direct-path)**: SDP body is clean — zero pod-network IPs in any `c=` line, confirmed directly from the capture. The SIP **header** check initially failed as expected (the pre-existing `Record-Route`/`Contact` pod-IP leak ADR 001 found by static analysis) — **now closed**: `topos`/`topos_htable` (ADR 003) is implemented, and the header check passes clean on a live call. See "topos and the AC5 load test" below.
- **AC5 (scale/rollout, no pool exhaustion)**: scaled FreeSWITCH 2→4→2. Every pod got a unique `net1` IP from the pool (`.128`-`.131`), no allocation conflicts, no stuck `Terminating` pods on scale-down. The heavier variant (concurrent calls in flight during the scale event) is now also done — see below.
- **AC6 (hold/unhold)**: confirmed. A real call with two in-dialog re-INVITEs (hold, then unhold) both got `200 OK` through Kamailio's loose-routing, with a clean `BYE`/`200` after.

**Three real bugs this live pass caught and fixed** (not caught by static review):
1. `config/freeswitch/entrypoint.sh`'s final `exec` targeted `/usr/local/bin/docker-entrypoint.sh`, based on a Dockerfile-source lookup that turned out not to match the actual published `1.11.3` image - `podman inspect` showed `Entrypoint: []`, `Cmd: ["/usr/bin/freeswitch"]`, and `/usr/local/bin` empty. Fixed to `exec /usr/bin/freeswitch` directly.
2. `charts/dispatcher-sync` had no toleration for the new, merged `voice-node` taint. Before this change, one lab worker (the old `media` pool) was untainted and absorbed ordinary workloads; after merging edge+media into one uniformly-tainted voice-node pool, `dispatcher-sync` had zero nodes left to schedule on (`FailedScheduling: 0/4 nodes are available`). Fixed by adding a toleration (no `nodeSelector`/NAD - it doesn't need `net1`, just permission to coexist on the same nodes).
3. `config/freeswitch/build.sh`'s `ctr images import` silently failed on every node ("image might be filtered out", no other error) because the upstream base image is `amd64`-only and this lab host is arm64 - containerd's default import filters non-native-platform images. Fixed by adding `--all-platforms`.

## topos and the AC5 load test

Done in a later pass, after the above: `topos`/`topos_htable` implemented per [ADR 003](003-topos-state-store.md) (user chose `topos_htable` - no new infrastructure), and the full AC5 load test (concurrent calls in flight during a FreeSWITCH scale-up/scale-down) actually run.

The first load test run found 5 real dropped calls out of 10 - not noise, three distinct, confirmed bugs, all fixed:

4. `environments/local/values.yaml`'s `dispatcher` list was a hardcoded pair of IPs (`10.244.3.35`, `10.244.3.36`) left over from before `dispatcher-sync` existed, still baked into Kamailio's dispatcher.list ConfigMap on every boot. Those pods had been gone for a long time; `ds_select_dst()` would still pick them on a fresh Kamailio restart, and calls sent there got a real `503` back (remapped to `500` by Kamailio's `tm` module) or no reply at all. Fixed: seed is now `dispatcher: []` - `dispatcher-sync` is the live source of truth and converges within one poll tick.
5. That fix exposed a second, previously-latent bug in `config/dispatcher-sync/sync.py`: `dispatcher.list` returns an HTTP 500 ("No Destination Sets") when a group has no destinations yet - legitimate, not an error - but `current_dispatcher_ips()` treated any `dispatcher.list` failure as fatal and made `sync_target()` give up for the whole tick, including never calling `dispatcher.add`. With a static non-empty seed this never mattered (the set always existed); with an empty seed it deadlocked permanently. Fixed: that specific 500 is now treated as "current = empty set."
6. The remaining failures were real `503`s from a single pod - a brand-new replica from the scale-up. `charts/freeswitch` had no `readinessProbe` (a gap [ADR 001](001-necessity-review.md) had already flagged but left unfixed), so Kubernetes marked it Ready, and `dispatcher-sync` added it, the instant the container started - before Sofia had bound its SIP socket. A call landing in that window got rejected. Fixed: a `readinessProbe` (`fs_cli -x "sofia status profile internal" | grep -q '^URL'`) now gates readiness on Sofia actually being up.

Re-run 3 times after all three fixes: 30/30 calls succeeded, 0 failures.

A real call recording (`test/sipp/call-recording-check.sh`, committed under `test/sipp/recordings/`) proves the full lifecycle - answer, real audio, hold (RTP gap confirmed in the packet trace), unhold, more real audio, hangup - with clean SIP headers throughout, against this fixed, `topos`-enabled stack.

## Open questions

1. FreeSWITCH's pool sizing formula assumes no autoscaling yet (`maxReplicas` is just today's `replicas: 2`); once autoscaling lands, `maxReplicas` needs to track the real ceiling, not the current count.
2. The base FreeSWITCH image (`ghcr.io/patrickbaus/freeswitch-docker:1.11.3`) is `amd64`-only; on this arm64 lab it runs under emulation (`ctr images import --all-platforms` was needed to load it at all). Kamailio's own base image, by contrast, has a native arm64 build. Worth flagging for production node architecture planning.
3. `topos_htable` mappings don't survive a Kamailio restart or share across Kamailio A/B (the accepted tradeoff in ADR 003) - not exercised live (no restart-mid-call test run).
4. The load test used 10 calls at 1/s with 2 scale events; a larger/sustained load profile hasn't been tried.
