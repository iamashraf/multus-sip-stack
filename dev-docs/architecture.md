# Architecture

This is a SIP voice stack on RKE2. An SBC hands calls to Kamailio, which dispatches them to a FreeSWITCH pool. The SBC and the chosen FreeSWITCH pod anchor RTP directly over the voice network, with no media relay in the cluster (see [ADR 002](adr/002-remove-rtpengine.md)). The design exists because standard Kubernetes networking (Services, kube-proxy, overlay NAT) can't carry a SIP or RTP session without breaking it.

[Interactive runtime architecture diagram](../docs/runtime-architecture.html). Explore, in a browser, how SIP signaling and media run on Kubernetes without `hostNetwork`, with dynamic dispatcher membership and graceful call drain.

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

FreeSWITCH touches the voice network directly: it anchors media with the SBC itself, instead of going through a relay (see ADR 002). The voice-pool NAD's sizing tracks FreeSWITCH's own replica ceiling, surge, and headroom (`charts/voice-network/values.yaml`), not a fixed relay pool.

## Why standard Kubernetes patterns don't work here

- **SIP and SDP carry addresses in the payload.** Services and kube-proxy rewrite packet headers, not bodies. A pod advertising its pod IP in SDP gets media sent to an unreachable address.
- **RTP needs a port range.** Services can't expose one ([kubernetes/kubernetes#23864](https://github.com/kubernetes/kubernetes/issues/23864)). Exposing ports one by one doesn't scale.
- **No SIP-aware ingress exists.** Nothing understands SIP dialogs or SDP well enough to route them through an Ingress or a Gateway.
- **L4 load balancers break server-initiated SIP.** Inbound requests work. But re-INVITE, BYE, and OPTIONS requests that Kamailio originates leave with the node IP instead of the VIP, so the SBC drops them.
- **`hostNetwork: true` is the common workaround, and a bad one.** It ties an IP to a node, allows only one instance per node, and needs privileged networking.
- **Pods are ephemeral. Calls aren't.** A rollout with no draining kills live calls.
- **Dispatcher membership is static by default**, but pods come and go.
- **CPU is a poor autoscaling signal for call load.** Active channels and CPS are what matter.

## Design principles

1. Isolate the hard part. Only the voice-node tier, Kamailio and FreeSWITCH, touches the voice network. `dispatcher-sync` and everything else stays on the ordinary pod network.
2. A pod owns its address. The IP in SDP and the packet's source IP match in both directions, with no NAT on the voice path.
3. Don't use `hostNetwork` or privileged pods without an explicit, approved exception.
4. Use stable identity for signaling: the SBC allow-lists Kamailio's IPs. Use pooled identity for media: FreeSWITCH's `net1` pool.
5. Treat calls as first-class lifecycle state. Nothing terminates while it still holds active calls, except after a bounded timeout.
6. Membership comes from Kubernetes EndpointSlices, never a hand-edited list.
7. Autoscaling uses call metrics (active channels, CPS), not CPU.
8. Every component exposes Prometheus metrics. Where practical, SIP and RTP quality metrics go through OpenTelemetry.
9. Everything is declarative: Helm charts, one `values.yaml` per environment, no `kubectl edit`.

## Build order

| Phase | Builds | Proves |
|---|---|---|
| 0 | This doc and the prerequisites checklist | Scope agreed before any infra |
| L | Local kind lab (SIPp SBC, simulated voice VLAN, Multus and Whereabouts) | Same charts. Only `values.yaml` differs by environment. Develop and test Phases 1-5 without touching real hardware |
| 1 | Multus and Canal cluster networking, NADs, voice node taint | A pod can own a routable `net1` IP and reach the SBC subnet with no pod-network IP ever appearing on the VLAN |
| 2 | Kamailio | SBC OPTIONS gets 200 OK from both instances |
| 3 | ~~media relay~~ | Removed. See [ADR 002](adr/002-remove-rtpengine.md). FreeSWITCH anchors media with the SBC directly instead (Phase 4) |
| 4 | FreeSWITCH, with its own `net1` | Two-way audio end to end, directly between the SBC and FreeSWITCH. No pod-network IP appears in any SIP header or in SDP |
| 5 | Dynamic membership and graceful drain | Scaling and rolling FreeSWITCH drops zero calls |
| 6 | ~~Go operator (CRDs, webhook, reconcilers)~~ | Evaluated and removed. See [ADR 001](adr/001-necessity-review.md). `dispatcher-sync` (Phase 5) is the kept implementation |
| 7 | Autoscaling and observability | Load ramp scales out before quality degrades. The dashboard shows the full call path |

Each phase's full acceptance criteria are in the project brief, not duplicated here.

## Dispatcher sync (Phase 5)

`charts/dispatcher-sync` closes the hand-edited dispatcher list gap. It runs
a small poll loop, `config/dispatcher-sync/sync.py`, written in Python's
standard library only and packaged in the Kamailio image. The loop keeps
both Kamailio instances' live dispatcher sets matched to the FreeSWITCH
pool, with no Go operator and no restart needed on either side. A
`controller-runtime`-based Go operator was built as an alternative and
removed after [ADR 001](adr/001-necessity-review.md)'s necessity review.
`dispatcher-sync` is the kept implementation.

- `charts/dispatcher-sync` also creates a headless `freeswitch` Service
  (`clusterIP: None`, selector `app=freeswitch`), only so Kubernetes
  generates a watchable EndpointSlice. Nothing routes calls through this
  Service.
- Every `pollIntervalSeconds` (default 3s), the loop reads the ready pod IPs
  from that EndpointSlice. It reconciles each Kamailio instance's *live*
  `dispatcher.list`, using the `dispatcher.add` and `dispatcher.remove`
  JSON-RPC methods, to match the EndpointSlice, not what it last pushed. So
  a Kamailio pod that restarts and comes back up from its static ConfigMap
  gets corrected on the next tick, the same way a FreeSWITCH scale event
  does.
- A failed EndpointSlice read skips the tick, instead of treating the
  failure as no backends being available. This way, one API blip can't
  empty both dispatchers. A failed RPC call against one Kamailio instance
  is logged and retried on the next tick. It doesn't stop the other
  instance from being synced.
- Verified live against the kind lab. Scaling FreeSWITCH up and down, and
  deleting a FreeSWITCH pod with `kubectl delete`, both update both
  Kamailio instances within one poll tick, with no manual step. Deleting a
  Kamailio pod, or manually drifting its in-memory dispatcher set, both
  self-heal the same way.
- FreeSWITCH has a `readinessProbe` that checks Sofia's `internal` profile
  is bound, so Kubernetes doesn't add a new pod to the dispatcher before
  SIP is actually listening. A live load test caught real dropped calls
  from this exact gap. Adding the probe closed it (see
  [ADR 002](adr/002-remove-rtpengine.md)).

## Decisions made

- Media path: the SBC anchors media directly with FreeSWITCH's own `net1`, with no media relay in the cluster. See [ADR 002](adr/002-remove-rtpengine.md), which supersedes the deferred "media relay vs. Calico BGP" question below.
- Dispatcher sync: `dispatcher-sync`, a Python poll loop, is kept. The `dispatcher-operator` Go alternative was evaluated and removed. See [ADR 001](adr/001-necessity-review.md).
- Kamailio topology hiding (`topos`): implemented with `topos_htable`, in-memory, with no new infrastructure. It closes the SIP-header pod-IP leak that ADR 001 found. See [ADR 003](adr/003-topos-state-store.md).

## Decisions deferred to ADRs

Tracked in `dev-docs/adr/`, written when each choice is made, not before:

- ipvlan vs. macvlan for the voice NAD. Default is ipvlan L2. Use macvlan only if the network team confirms no per-port MAC limit.
- TLS and SRTP: out of scope through Phase 5. Where termination would happen, if added.
- Kamailio dialog and registration state sharing (DMQ vs. shared DB), if needed.
- Calico BGP (routable pod IPs) as an alternative to the whole Multus and Whereabouts stack. This is a lower-priority question, since FreeSWITCH already owns `net1` directly and keeps RTP off the pod network either way.

## Non-goals

- Public internet SIP exposure (the SBC owns the perimeter).
- WebRTC clients (separate STUNner path).
- Transcoding at scale (SR-IOV noted as a future option only).
