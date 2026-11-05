# Architecture

A SIP voice stack on RKE2: an SBC hands calls to Kamailio, which dispatches to a FreeSWITCH pool; the SBC and the chosen FreeSWITCH pod anchor RTP directly over the voice network, with no media relay in the cluster (see [ADR 002](adr/002-remove-rtpengine.md)). The design exists because standard Kubernetes networking (Services, kube-proxy, overlay NAT) cannot carry a SIP/RTP session without breaking it.

[Interactive runtime architecture diagram](../docs/runtime-architecture.html) — how SIP signaling and media run on Kubernetes without hostNetwork, dynamic dispatcher membership, and graceful call drain, explorable in a browser.

## Call path

```
                 Voice VLAN (e.g. 192.168.50.0/24)
   +-------+     SIP           +-------------------+
   |  SBC  | ----------------> | Kamailio A / B    |  static IPs on net1
   |       |                   | (dispatcher)      |
   |       |                   +-------------------+
   |       |                            | eth0 (pod network: Canal)
   |       |                            v
   |       |     RTP           +----------------------+
   |       | <----------------> | FreeSWITCH pool      |  pooled IPs on net1,
   +-------+                   | (Deployment, scaled) |  ordinary pods otherwise
                                +----------------------+
```

| Tier | Components | Network | Privilege |
|---|---|---|---|
| Voice | Kamailio, FreeSWITCH | `net1` (voice VLAN, Multus) + `eth0` (pod network) | Scheduled on tainted voice nodes, no hostNetwork |

FreeSWITCH now touches the voice network directly, since it anchors media with the SBC itself instead of going through a relay (see ADR 002). The voice-pool NAD's sizing tracks FreeSWITCH's own replica ceiling, surge, and headroom (`charts/voice-network/values.yaml`), not a fixed relay pool.

## Why standard Kubernetes patterns don't work here

- **SIP/SDP carry addresses in the payload.** Services and kube-proxy rewrite packet headers, not bodies — a pod advertising its pod IP in SDP gets media sent to an unreachable address.
- **RTP needs a port range.** Services can't expose one ([kubernetes/kubernetes#23864](https://github.com/kubernetes/kubernetes/issues/23864)); exposing ports one by one doesn't scale.
- **No SIP-aware ingress exists.** Nothing understands SIP dialogs or SDP to route through an Ingress/Gateway.
- **L4 load balancers break server-initiated SIP.** Inbound requests work; re-INVITE/BYE/OPTIONS that Kamailio originates leave with the node IP, not the VIP, and the SBC drops them.
- **`hostNetwork: true` is the common workaround and a bad one.** It ties an IP to a node, allows one instance per node, and needs privileged networking.
- **Pods are ephemeral, calls aren't.** A rollout with no draining kills live calls.
- **Dispatcher membership is static by default**, but pods come and go.
- **CPU is a poor autoscaling signal** for call load; active channels and CPS are what matter.

## Design principles

1. Isolate the hard part — only the voice-node tier (Kamailio, FreeSWITCH) touches the voice network; dispatcher-sync and everything else stays on the ordinary pod network.
2. A pod owns its address — the IP in SDP and the source IP of every packet match, in both directions, no NAT on the voice path.
3. No `hostNetwork`, no privileged pods, without an explicit, approved exception.
4. Stable identity for signaling (SBC-whitelisted Kamailio IPs), pooled identity for media (FreeSWITCH's `net1` pool).
5. Calls are first-class lifecycle state — nothing terminates while holding active calls, up to a bounded timeout.
6. Membership derives from Kubernetes (EndpointSlices), never a hand-edited list.
7. Autoscaling uses call metrics (active channels, CPS), not CPU.
8. Every component exposes Prometheus metrics; SIP/RTP quality via OpenTelemetry where practical.
9. Everything is declarative: Helm charts, one `values.yaml` per environment, no `kubectl edit`.

## Build order

| Phase | Builds | Proves |
|---|---|---|
| 0 | This doc + prerequisites checklist | Scope agreed before any infra |
| L | Local kind lab (SIPp SBC, simulated voice VLAN, Multus+Whereabouts) | Same charts, environment-only `values.yaml` diff — develop and test Phases 1-5 without touching real hardware |
| 1 | Multus/Canal cluster networking, NADs, voice node taint | A pod can own a routable `net1` IP and reach the SBC subnet with no pod-network IP ever appearing on the VLAN |
| 2 | Kamailio | SBC OPTIONS gets 200 OK from both instances |
| 3 | ~~media relay~~ | Removed — see [ADR 002](adr/002-remove-rtpengine.md). FreeSWITCH anchors media with the SBC directly instead (Phase 4) |
| 4 | FreeSWITCH, with its own `net1` | Two-way audio end to end, directly between the SBC and FreeSWITCH; no pod-network IP in any SIP header or SDP |
| 5 | Dynamic membership + graceful drain | Scaling and rolling FreeSWITCH drops zero calls |
| 6 | ~~Go operator (CRDs, webhook, reconcilers)~~ | Evaluated and removed — see [ADR 001](adr/001-necessity-review.md). `dispatcher-sync` (Phase 5) is the kept implementation |
| 7 | Autoscaling + observability | Load ramp scales out before quality degrades; dashboard shows the full call path |

Each phase's full acceptance criteria are in the project brief, not duplicated here.

## Dispatcher sync (Phase 5)

`charts/dispatcher-sync` closes the "hand-edited dispatcher list" gap: a small
poll loop (`config/dispatcher-sync/sync.py`, Python stdlib only, reuses the
Kamailio image) that keeps both Kamailio instances' live dispatcher sets
matched to the FreeSWITCH pool, with no Go operator and no restart needed on
either side. A `controller-runtime`-based Go operator was built as an
alternative and removed after [ADR 001](adr/001-necessity-review.md)'s
necessity review — `dispatcher-sync` is the kept implementation.

- `charts/dispatcher-sync` also creates a headless `freeswitch` Service
  (`clusterIP: None`, selector `app=freeswitch`) purely so Kubernetes
  generates a watchable EndpointSlice - nothing routes calls through it.
- Every `pollIntervalSeconds` (default 3s), the loop reads the ready pod IPs
  from that EndpointSlice and reconciles each Kamailio instance's *live*
  `dispatcher.list` (via the `dispatcher.add`/`dispatcher.remove` JSON-RPC
  methods) to match - not against what it last pushed, so a Kamailio pod
  that restarts and reboots from its static ConfigMap gets corrected on the
  next tick, same as a FreeSWITCH scale event.
- A failed EndpointSlice read skips the tick rather than treating it as "no
  backends" - one API blip must not empty both dispatchers. A failed RPC
  call against one Kamailio instance is logged and retried next tick; it
  doesn't stop the other instance from being synced.
- Verified live against the kind lab: scaling FreeSWITCH up/down and
  `kubectl delete`-ing a FreeSWITCH pod both update both Kamailio instances
  within one poll tick with no manual step; deleting a Kamailio pod and
  manually drifting its in-memory dispatcher set both self-heal the same way.
- FreeSWITCH has a `readinessProbe` (checks Sofia's `internal` profile is
  bound) so a new pod isn't added to the dispatcher before SIP is actually
  listening — closed after a live load test caught real dropped calls from
  this exact gap (see [ADR 002](adr/002-remove-rtpengine.md)).

## Decisions made

- Media path: the SBC anchors media directly with FreeSWITCH's own `net1`; no media relay in the cluster. See [ADR 002](adr/002-remove-rtpengine.md) (supersedes the deferred "media relay vs. Calico BGP" question below).
- Dispatcher sync: `dispatcher-sync` (Python poll loop) kept; the `dispatcher-operator` Go alternative evaluated and removed. See [ADR 001](adr/001-necessity-review.md).
- Kamailio topology hiding (`topos`): implemented with `topos_htable` (in-memory, no new infrastructure) — closes the SIP-header pod-IP leak ADR 001 found. See [ADR 003](adr/003-topos-state-store.md).

## Decisions deferred to ADRs

Tracked in `dev-docs/adr/`, written when each choice is made, not before:

- ipvlan vs. macvlan for the voice NAD (default ipvlan L2; macvlan only if the network team confirms no per-port MAC limit).
- TLS/SRTP: out of scope through Phase 5; where termination would happen if added.
- Kamailio dialog/registration state sharing (DMQ vs. shared DB), if needed.
- Calico BGP (routable pod IPs) as an alternative to the whole Multus/Whereabouts stack — now a lower-priority question since FreeSWITCH owning `net1` directly already keeps RTP off the pod network either way.

## Non-goals

- Public internet SIP exposure (the SBC owns the perimeter).
- WebRTC clients (separate STUNner path).
- Transcoding at scale (SR-IOV noted as a future option only).
