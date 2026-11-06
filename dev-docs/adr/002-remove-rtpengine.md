# ADR 002: Remove rtpengine

Status: accepted.

ADR 002 supersedes the item previously listed as deferred in `dev-docs/architecture.md`: "Media path: rtpengine bridging `net1`/`eth0` vs. routable pod IPs (Calico BGP) with no rtpengine".

## Decision

This ADR removes rtpengine from both the design and the implementation. The SBC is now the only media relay. Media runs **SBC <-> Kamailio** (SIP only) and **SBC <-> FreeSWITCH `net1`** (RTP, direct).

## Why

rtpengine existed to solve one problem: it made media reachable when the far end couldn't reach FreeSWITCH. That problem doesn't exist here.

1. **The SBC already anchors media.** It terminates carrier RTP and starts a new stream toward us. [ADR 001](001-necessity-review.md)'s necessity review confirmed that a second relay inside the cluster duplicated that same job.
2. **FreeSWITCH can own a reachable address.** With Multus, a FreeSWITCH pod gets a `net1` interface on the voice network. The SBC reaches the IP that FreeSWITCH puts in its SDP directly, and replies leave from that same IP. There's no NAT in the path, so nothing needs to rewrite SDP.
3. **The port range problem goes away.** The SBC sends RTP straight to the FreeSWITCH pod IP. No Kubernetes Service, no port exposure, no range needed in Kubernetes.
4. **It added cost with no benefit here.** It added an extra hop in the media path: more latency, and another place to drop packets. It was another component to scale, drain, and monitor. It was also another failure point: one rtpengine pod ran unreplicated, so if it died, every call routed through it lost audio.
5. **It didn't improve resilience.** If a FreeSWITCH pod dies, its calls drop with or without rtpengine.

**Correction to the original premise.** As built in this repo, rtpengine ran plain userspace forwarding: no kernel module, no added Linux capabilities, and no privileged container (`config/rtpengine/entrypoint.sh` and `charts/rtpengine/templates/deployment.yaml`, both now removed). The "needs a host kernel module and extra privileges" cost would have applied only if someone turned on in-kernel offload later. That cost was never a defect this removal needed to fix, just a correction to the original premise.

## Pre-check (done before any change)

- No transcoding, SRTP or DTLS bridging, WebRTC, or recording logic depended on rtpengine. Every hit for those terms was stock FreeSWITCH default-config boilerplate, unrelated to rtpengine.
- `dispatcher-sync` never synced anything rtpengine-related, so there was nothing to remove there.
- No rtpengine drain logic, PodDisruptionBudget, metrics, dashboards, or `make images` target existed.
- One real test-asset dependency: `local/sbc/scenarios/uac-demo.xml` primed "symmetric RTP learning," a real rtpengine behavior (it won't relay to an address it hasn't received a packet from). That behavior doesn't apply once FreeSWITCH is a direct, non-NATed destination. This change corrected the scenario's comment and left the harmless warm-up packets in place.
- `dev-docs/prerequisites.md`'s SBC and firewall assumptions ("pool sized for peak sessions," "RTP port range -> the pool") carried over cleanly to the FreeSWITCH pool.

## What was removed

- Deleted `charts/rtpengine/` and `config/rtpengine/` entirely.
- Renamed the `rtpengine.pool` values to `freeswitch.pool` in `charts/voice-network`, since the same NAD now serves FreeSWITCH.
- Removed the `RTPENGINE_HOST` env var and the `rtpengineHost` value from Kamailio.
- Removed `loadmodule "rtpengine.so"`, its `modparam`, every `rtpengine_manage()` call, and the `onreply_route[MANAGE_REPLY]` hook from `kamailio.cfg.tmpl`. Kamailio is SIP-only now. Dispatch, `record_route()`, and the `force_send_socket()` dual-homing logic stay, because SIP still needs to move between `net1` and `eth0`.
- Removed all rtpengine references from `README.md`, `dev-docs/architecture.md`, `dev-docs/local-lab.md`, `dev-docs/verification.md`, `dev-docs/prerequisites.md`, `dev-docs/versions.md`, and `local/Makefile`.
- Rewrote `test/sipp/media-flow-check.sh` from a two-leg check (SBC->rtpengine, rtpengine->FreeSWITCH) to a single-leg check that goes directly between the SBC and FreeSWITCH.

## What changed

**FreeSWITCH** now owns a `net1` address and anchors media itself:
- Attaches `voice-pool` via the Multus annotation (`k8s.v1.cni.cncf.io/networks: voice-pool`).
- A new `config/freeswitch/entrypoint.sh` script reads `net1` and `eth0` at startup and fails fast with a clear log line if `net1` is missing. Before starting FreeSWITCH, it also checks that `ip route get $SBC_MEDIA_IP` resolves via `net1`. The script is built into a thin wrapper image (`config/freeswitch/Dockerfile`) over the upstream `ghcr.io/patrickbaus/freeswitch-docker:1.11.3`.
- The Sofia profile (`config/freeswitch/internal.xml`) now sets `sip-ip` and `ext-sip-ip` to read `$${sip_ip_v4}`, which resolves to `eth0`, and sets `rtp-ip` and `ext-rtp-ip` to read `$${rtp_ip_v4}`, which resolves to `net1`. In `config/freeswitch/vars.xml.tmpl`, `external_rtp_ip` and `external_sip_ip` get a plain `__NET1_IP__` substitution. That file was renamed from `vars.xml` because it's now templated the same way as `kamailio.cfg.tmpl`. These values were verified directly against the running config already in this repo, rather than against external docs.
- This change renamed scheduling from "edge nodes" to **voice nodes** everywhere. The taint is now `voice-node=true:NoSchedule`, and the label is `voice-role: voice`. FreeSWITCH carries both the toleration and the matching `nodeSelector`. It previously used an untainted, differently labeled `media` pool.

**Network attachment** (`charts/voice-network`):
- `freeswitch.pool.rangeStart` and `freeswitch.pool.rangeEnd` replace `rtpengine.pool.*`.
- `charts/voice-network/templates/nad-pool.yaml` now fails `helm template` or `helm install` with a clear message if the pool is smaller than `freeswitch.maxReplicas + maxSurge + poolHeadroom`. These are new values, documented in `charts/voice-network/values.yaml`. Shortcut: assumes `rangeStart` and `rangeEnd` share one /24, and doesn't support a range that spans a subnet boundary.

**Kamailio** is SIP-only now. `record_route()` stays. Topology hiding (`topos`) was proposed separately in [ADR 003](003-topos-state-store.md) and was not added in this pass, per the instruction to ask before adding its state store. A later pass added it. See "topos and the AC5 load test" below.

**Local lab**: all three kind workers (not two) attach to the `voice` Docker network (`local/voice-net.sh`, `local/kind.yaml`), since FreeSWITCH needs it now too. `local/addons.sh`'s CNI-plugin-copy step now targets all `voice-role=voice` nodes instead of just the former edge pair.

**Tests**:
- `local/check-addresses.sh` now scans the full decoded SIP and SDP message (`tshark -V`) instead of only `sip.msg_body`. This closes the header-leak gap that [ADR 001](001-necessity-review.md) found: pod IPs were already leaking into `Record-Route`, `Contact`, and `Via` before this change, independent of rtpengine.
- `test/sipp/media-flow-check.sh` now captures only from the SBC (one leg, since there's only one hop) and asserts RTP flows both directions directly between the SBC and FreeSWITCH's `net1` address.

## What we give up

- FreeSWITCH now needs voice-VLAN nodes, not just ordinary pod-network nodes. That means a bigger footprint on the constrained VLAN-attached node pool.
- A larger IP block: the pool now has to cover FreeSWITCH's replica ceiling plus rollout surge plus headroom, not a small fixed relay pool.
- No central point for media-layer stats remains. rtpengine's `rtpengine-ng` control port was a natural place to pull jitter and loss numbers from, across every call. That data now has to come from FreeSWITCH's own call variables, per pod. `make quality` already planned to gather that data this way, regardless of this change.
- A `topos` state store. This wasn't previously needed, because rtpengine's own SDP rewriting incidentally also kept FreeSWITCH's address out of Kamailio's visible SIP headers, to some degree. That rewriting never fully worked (see ADR 001), so this is a pre-existing gap that this change closes properly, not a new cost that this change caused. A later pass implemented it via `topos_htable`, with no new infrastructure. See ADR 003.

## Triggers that would bring a media relay back

Re-evaluate (new ADR, don't just flip a flag) if any of these shows up:
- WebRTC or browser clients that need ICE and DTLS-SRTP termination.
- Carriers that connect without an SBC in front of them.
- Media pods scheduled on nodes without the voice VLAN NIC.
- Media forking for recording outside what FreeSWITCH and the SBC can do themselves.

The `charts/rtpengine` chart is deleted, not merely disabled by default. If one of these triggers fires, rebuild the chart (or restore it from version control history) instead of re-enabling it with a flag.

## Verification

**Static**: `helm template` and `helm lint` on all 4 remaining charts render clean with `environments/local/values.yaml`, including the pool-size guard. Testing confirmed the guard both passes at the real range and fails with a clear message when the range shrinks below the minimum. `grep -ri rtpengine` across the repo returns nothing outside the ADRs and 3 explained exceptions: an ADR filename in a link, a deliberately preserved historical record in `verification.md`, and the two generated HTML diagrams, which weren't hand-edited here.

**Live** (kind lab recreated with the new `kind.yaml`/topology, `kamailio` and `freeswitch` images rebuilt and kind-loaded, all 4 charts installed):

- **AC3 (direct audio)**: confirmed. A real call through `test/sipp/media-flow-check.sh` showed RTP flowing both directions directly between the SBC (`192.168.50.5`) and the FreeSWITCH pod that took the call (`192.168.50.128`, its own `net1`): 249 packets one way, 244 the other. No relay IP appeared anywhere in the capture. `fs_cli -x "sofia status profile internal"` confirmed that `RTP-IP` and `Ext-RTP-IP` equaled the `net1` address, and that `SIP-IP` and `Ext-SIP-IP` equaled the `eth0` address, exactly as designed.
- **AC4 (address, interface, and direct path)**: the SDP body is clean. The capture confirmed zero pod-network IPs in any `c=` line, directly. The SIP header check initially failed, as expected, because of the pre-existing `Record-Route` and `Contact` pod-IP leak that ADR 001 found by static analysis. `topos` and `topos_htable` (ADR 003) now close that leak, and the header check passes clean on a live call. See "topos and the AC5 load test" below.
- **AC5 (scale and rollout, no pool exhaustion)**: FreeSWITCH scaled from 2 to 4 and back to 2. Every pod got a unique `net1` IP from the pool (`.128`-`.131`), with no allocation conflicts and no pods stuck in `Terminating` on scale-down. The heavier variant of this test, with concurrent calls in flight during the scale event, is also done now. See below.
- **AC6 (hold and unhold)**: confirmed. A real call sent two in-dialog re-INVITEs, one for hold and one for unhold. Both got a `200 OK` through Kamailio's loose routing, with a clean `BYE`/`200` after.

**Three real bugs this live pass caught and fixed** (not caught by static review):
1. `config/freeswitch/entrypoint.sh`'s final `exec` targeted `/usr/local/bin/docker-entrypoint.sh`, based on a Dockerfile-source lookup that turned out not to match the actual published `1.11.3` image. `podman inspect` showed `Entrypoint: []`, `Cmd: ["/usr/bin/freeswitch"]`, and an empty `/usr/local/bin`. The fix changed the `exec` to target `/usr/bin/freeswitch` directly.
2. `charts/dispatcher-sync` had no toleration for the new, merged `voice-node` taint. Before this change, one lab worker (the old `media` pool) was untainted and absorbed ordinary workloads. After merging edge and media into one uniformly tainted voice-node pool, `dispatcher-sync` had zero nodes left to schedule on (`FailedScheduling: 0/4 nodes are available`). The fix added a toleration, with no `nodeSelector` or NAD, since `dispatcher-sync` doesn't need `net1`, just permission to coexist on the same nodes.
3. `config/freeswitch/build.sh`'s `ctr images import` silently failed on every node ("image might be filtered out", no other error) because the upstream base image is `amd64`-only and this lab host is arm64. Containerd's default import filters non-native-platform images. The fix added the `--all-platforms` flag.

## topos and the AC5 load test

A later pass, after everything above, implemented `topos` and `topos_htable` per [ADR 003](003-topos-state-store.md) (the user chose `topos_htable`, no new infrastructure) and actually ran the full AC5 load test, with concurrent calls in flight during a FreeSWITCH scale-up and scale-down.

The first load test run found 5 real dropped calls out of 10. That wasn't noise: it traced to three distinct, confirmed bugs, all fixed:

4. `environments/local/values.yaml`'s `dispatcher` list was a hardcoded pair of IPs (`10.244.3.35`, `10.244.3.36`) left over from before `dispatcher-sync` existed. It was still baked into Kamailio's dispatcher.list ConfigMap on every boot. Those pods had been gone for a long time, but `ds_select_dst()` would still pick them on a fresh Kamailio restart, and calls sent there got a real `503` back (remapped to `500` by Kamailio's `tm` module) or no reply at all. The fix: the seed is now `dispatcher: []`, so `dispatcher-sync` is the live source of truth and converges within one poll tick.
5. That fix exposed a second, previously latent bug in `config/dispatcher-sync/sync.py`: `dispatcher.list` returns an HTTP 500 ("No Destination Sets") when a group has no destinations yet. That's legitimate, not an error, but `current_dispatcher_ips()` treated any `dispatcher.list` failure as fatal and made `sync_target()` give up for the whole tick, including never calling `dispatcher.add`. With a static, non-empty seed this never mattered, because the set always existed. With an empty seed, it deadlocked permanently. The fix: that specific 500 is now treated as "current = empty set."
6. The remaining failures were real `503`s from a single pod, a brand-new replica from the scale-up. `charts/freeswitch` had no `readinessProbe`, a gap [ADR 001](001-necessity-review.md) had already flagged but left unfixed. Without it, Kubernetes marked the pod Ready, and `dispatcher-sync` added it, the instant the container started, before Sofia had bound its SIP socket. A call that landed in that window got rejected. The fix: a `readinessProbe` (`fs_cli -x "sofia status profile internal" | grep -q '^URL'`) now gates readiness on Sofia actually being up.

Re-run 3 times after all three fixes, the test passed 30 of 30 calls, with 0 failures.

A real call recording (`test/sipp/call-recording-check.sh`, committed under `test/sipp/recordings/`) proves the full lifecycle: answer, real audio, hold with an RTP gap confirmed in the packet trace, unhold, more real audio, and hangup. SIP headers stay clean throughout, on this fixed, `topos`-enabled stack.

## Open questions

1. FreeSWITCH's pool sizing formula assumes no autoscaling yet: `maxReplicas` is just today's `replicas: 2`. Once autoscaling lands, `maxReplicas` needs to track the real ceiling, not the current count.
2. The base FreeSWITCH image (`ghcr.io/patrickbaus/freeswitch-docker:1.11.3`) is `amd64`-only. On this arm64 lab it runs under emulation, and loading it at all needed `ctr images import --all-platforms`. Kamailio's own base image, by contrast, has a native arm64 build. This is worth flagging for production node architecture planning.
3. `topos_htable` mappings don't survive a Kamailio restart or share across `kamailio-a` and `kamailio-b`, the accepted tradeoff in ADR 003. This restart-mid-call behavior hasn't been exercised live, since no restart-mid-call test has run.
4. The load test used 10 calls at 1/s with 2 scale events. A larger, sustained load profile hasn't been tried.
