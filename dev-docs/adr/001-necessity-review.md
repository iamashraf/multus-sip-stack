# ADR 001: Necessity review (Step 1 of the media-path addendum)

Status: proposed, awaiting decisions below.

This document reviews every component in the repo, or named in the build-order plan, against what it actually does today. It checks the repo itself, not what the architecture doc aspires to. The addendum asked for this review before any code or chart changes. This document is that review.

This file lives in `dev-docs/adr/`, the location `dev-docs/architecture.md:101` already names for ADRs. The addendum asked for `docs/adr/`. In this repo, `docs/` holds only the published HTML diagrams, so this file lives here instead.

## Summary

| Component | Verdict | Why |
|---|---|---|
| Multus | Required | Only way to give a pod a second, real voice-VLAN IP without `hostNetwork` |
| Whereabouts | Required | Pooled NAD needs cluster-aware IPAM. A too-narrow range already caused one real outage |
| ipvlan vs macvlan | Conditional | Picked per environment. The production choice depends on switch MAC-per-port support, which is unconfirmed |
| Static IPAM for Kamailio | Required | SBC needs a stable IP to whitelist across restarts |
| Kamailio | Required | Removing it re-opens the "hand-edited dispatcher list" problem the project exists to close |
| Two Kamailio instances | Required | A single instance is a signaling SPOF. It also surfaced a real rollout bug, described below |
| Dispatcher sync (`dispatcher-sync` vs `dispatcher-operator`) | One Required, one Remove | Both do the same job and template the same Service name. Pick one |
| rtpengine | Conditional | Required under today's design. Becomes optional once FreeSWITCH gets `net1`, which matches the addendum |
| Kamailio `topos` and state store | Required | Verified today: pod IPs already leak into SIP headers the SBC sees |
| Operator CRDs and mutating webhook | Remove | Never built. A Multus annotation does the job the plan credited to them |
| KEDA / custom-metrics autoscaling | Nice to have | Not built. No acceptance criterion needs automatic scaling |
| Prometheus / Grafana / OpenTelemetry | Nice to have (keep existing metrics) | Only a bare metrics endpoint exists. No server, dashboard, or tracing exists anywhere else |
| PodDisruptionBudgets | Required (real nodes) | The drain script and grace period exist and work. The PodDisruptionBudget itself doesn't exist yet |
| Node taints and labels, edge vs media pools | Required | Already load-bearing. Becomes mandatory once FreeSWITCH needs `net1` too |
| Canal vs Calico BGP | Remove from this addendum's scope | Already deferred to its own ADR. Moot once media never touches the pod network |
| MetalLB / any load balancer | Remove | Not present, not referenced, actively contradicts the "no L4 LB for SIP" design note |

## Multus

- **Purpose**: lets Kamailio and rtpengine pods carry a second NIC on the voice VLAN. Each pod still stays an ordinary, schedulable pod on the pod network.
- **Challenge it solves**: Services, kube-proxy, and overlay NAT can't carry SIP and SDP traffic, because the addresses live in the payload, not just in the headers. See `README.md` and the "why standard Kubernetes patterns don't work here" section of `dev-docs/architecture.md`.
- **What breaks without it**: the only way to put a real voice-VLAN IP on a pod is `hostNetwork`. `hostNetwork` ties the IP to a node, allows one instance per node, and needs privileged networking. All three violate design principle 3.
- **Alternatives**: `hostNetwork`, rejected above. SR-IOV, not evaluated, noted only as a future option in `architecture.md`'s non-goals. Calico BGP, which would make pod IPs directly routable and remove Multus entirely (see the Canal vs Calico BGP row).
- **Cost**: a DaemonSet, plus copying the macvlan, ipvlan, and static CNI binaries onto every node that needs them (`local/addons.sh`), plus one more annotation convention to keep consistent across charts.
- **Verdict**: Required, as long as voice-VLAN pods stay separate from the general pod network.
- **Evidence**: `local/addons.sh` installs it. Every voice NAD (`charts/voice-network/templates/nad-*.yaml`) depends on it. It's load-bearing today, not speculative.

## Whereabouts

- **Purpose**: cluster-wide IPAM for the pooled voice NAD, so two pods on different nodes never get handed the same IP.
- **Challenge it solves**: Multus's own `static` IPAM only fits a single fixed IP, used for Kamailio. A pool needs allocation that's safe across the whole cluster.
- **What breaks without it**: hand-rolling pool allocation means re-implementing Whereabouts' own cross-node locking. That's more code, not less.
- **Alternatives**: do-nothing or host-local IPAM, not cluster-aware and prone to duplicate IPs across nodes. A hand-maintained static list per pod, which doesn't scale with replica count.
- **Cost**: two more CRDs and a reconciler Deployment (`local/addons.sh`). A pool that can be mis-sized.
- **Verdict**: Required for any pooled voice-VLAN pod, rtpengine today and FreeSWITCH's `net1` under the new design.
- **Evidence**: `charts/voice-network/templates/nad-pool.yaml:15` sets `"type": "whereabouts"`. `environments/local/values.yaml:28-36` documents a real incident where a too-narrow range gave rtpengine the wrong subnet mask and broke reverse media. Not a hypothetical cost.

## ipvlan vs macvlan

- **Purpose**: the Linux interface mode the voice NAD uses.
- **Challenge it solves**: whether the physical switch allows multiple MAC addresses per port.
- **What breaks without picking correctly**: macvlan on a switch that enforces one MAC per port gets the interface's traffic dropped, or gets the port disabled.
- **Alternatives**: ipvlan, which shares the parent's MAC and works even when the switch restricts MACs per port. This is the stated production default. macvlan, used locally only because the Podman bridge has no such limit.
- **Cost**: none beyond a values flag. Already implemented as a ternary in the NAD template.
- **Verdict**: Conditional. A choice must be made, but which one is correct for production stays unverified until the network team confirms switch behavior.
- **Evidence**: `charts/voice-network/templates/nad-pool.yaml:1`. `dev-docs/prerequisites.md`'s checklist asks the network team this exact question.

## Static IPAM for Kamailio

- **Purpose**: a fixed `net1` IP per Kamailio instance, stable across restarts, for the SBC to whitelist.
- **Challenge it solves**: SBC trust models are usually IP-based. A Kamailio IP that changes on every pod restart means updating the SBC's config out of band every time.
- **What breaks without it**: the SBC either gets reconfigured on every Kamailio restart, or it has to trust a wider range than one specific dispatcher, which weakens its own ACL.
- **Alternatives**: a rotating pool of signaling IPs, which loses the "one whitelisted address" property. DNS-based SBC trust, uncommon and unverified for the SBC model in use here.
- **Cost**: one static NAD plus a per-instance `ips` annotation. Low, since it's one fixed value per Deployment, not a pool.
- **Verdict**: Required.
- **Evidence**: `charts/kamailio/templates/deployment.yaml:18`. `dev-docs/prerequisites.md`'s "Static IP for each Kamailio instance" item.

## Kamailio itself

- **Purpose**: SIP dispatch to a FreeSWITCH pool, Record-Route so in-dialog requests keep passing through it, and, today, a target for the dispatcher sync mechanism that keeps membership current.
- **Challenge it solves**: could the SBC dispatch to FreeSWITCH directly? That would need the SBC itself to track Kubernetes-driven, constantly-changing FreeSWITCH pod membership, and to keep in-dialog requests sticky across that churn. Almost no SBC does this natively. Most work off a static or DNS-resolved backend list.
- **What breaks without it**: the SBC falls back to a static or hand-maintained FreeSWITCH address list. That's exactly the "hand-edited dispatcher list" gap `dev-docs/architecture.md:73` says this project exists to close, and it directly contradicts design principle 6: membership derives from Kubernetes, never a hand-edited list.
- **Alternatives**: do nothing, keeping a static list on the SBC and losing dynamic membership. Teach the SBC to watch Kubernetes, which is outside this project's control, as the addendum itself calls out.
- **Cost**: two Deployments, config templating, and now a topology-hiding layer and state store on top.
- **Verdict**: Required.
- **Evidence**: `dev-docs/architecture.md:50,73`. Both `dispatcher-sync` and `dispatcher-operator` exist specifically to serve Kamailio's dispatcher list, nothing else.

## Two Kamailio instances vs one

- **Purpose**: redundant SIP signaling entry points, so the SBC has a second endpoint if one is unreachable.
- **Challenge it solves**: a single static-IP pod is a hard single point of failure for all new calls.
- **What breaks without it**: one instance down, whether from a node issue, a pod crash, or any rolling update, blacks out new call signaling until Kubernetes reschedules it. Rescheduling itself is slower than for a stateless pod, because the static IP and its NAD have to reattach.
- **A concrete defect this surfaced, not hypothetical**: `charts/kamailio/templates/deployment.yaml` sets no `strategy:` and `replicas: 1` per instance. The Deployment default, `RollingUpdate` with `maxSurge: 25%` rounding up to 1 pod, will briefly try to run two pods that both request the same static `net1` IP from the same `voice-static` NAD during any rollout. This needs `strategy.type: Recreate` regardless of this addendum's decisions. Noted here, not fixed here.
- **Alternatives**: one instance plus fast rescheduling, which accepts an outage window on every disruption. One instance behind a floating or VRRP IP, a different kind of complexity, not evaluated.
- **Cost**: doubles the static-IP footprint and the number of targets every dispatcher sync mechanism has to write to.
- **Verdict**: Required, for the stated HA goal.
- **Evidence**: `charts/kamailio/values.yaml` names `kamailio-a` and `kamailio-b`. `charts/kamailio/templates/deployment.yaml` sets no `strategy:` and reuses the same static IP annotation every rollout.

## Dispatcher sync: `dispatcher-sync` vs `dispatcher-operator`

Correction to the addendum's framing: `config/dispatcher-sync/sync.py` is not a sidecar. It is its own Deployment, `charts/dispatcher-sync`, and it reuses the Kamailio image.

`dispatcher-sync` and `dispatcher-operator` both exist in the repo right now and do the same job. Each reads FreeSWITCH's EndpointSlice and reconciles both Kamailio instances' live dispatcher sets against it. Both chart templates also create an identical headless `freeswitch` Service for the same reason, so the two cannot be installed into the same namespace without a name collision.

| | `dispatcher-sync` | `dispatcher-operator` |
|---|---|---|
| Language and runtime | Python stdlib, reuses the kamailio image | Go, controller-runtime, its own image |
| Reaction | 3s poll | EndpointSlice watch, 5s resync fallback |
| Metrics | none | 3 Prometheus metrics |
| RBAC | implicit (same SA as kamailio pods) | dedicated Role scoped to one namespace, no ClusterRole |
| Status | documented and live-verified in `architecture.md:71-97`. Not wired into `local/Makefile` | just merged. Two of the last four commits are hardening fixes: periodic resync and a namespace-scoped cache |

- **Challenge it solves**: same as Kamailio's row above, the "hand-edited dispatcher list" problem.
- **What breaks without either**: covered under Kamailio's row.
- **Alternatives**: push this logic into a Kamailio module directly, which means more code and wasn't evaluated. Keep both and rename one, which doesn't solve the Service name collision.
- **Cost**: `dispatcher-operator` is the heavier artifact: an extra Go module, its own image, a Chart, RBAC, and a Go test suite, but it's event-driven and has metrics. `dispatcher-sync` is lighter, with no new image, but it polls and has no observability.
- **Verdict**: one Required, one Remove. This is a recommendation, not a decision made here: keep `dispatcher-operator` and remove `dispatcher-sync`, since `dispatcher-operator` reacts faster, already has metrics (principle 8), and is the one actively being hardened. This is flagged as a decision for the user below, not decided unilaterally.
- **Evidence**: `charts/dispatcher-sync/templates/freeswitch-service.yaml` and `charts/dispatcher-operator/templates/freeswitch-service.yaml` serve an identical purpose. Git log commits `697df0b` and `acb857a` are `dispatcher-operator` hardening commits. `local/Makefile` wires neither chart in yet, so this collision hasn't been hit in CI, only inspected here.
- **Decision**: the project kept `dispatcher-sync` and removed `dispatcher-operator`, the reverse of the verdict above. `charts/dispatcher-operator` and `config/dispatcher-operator` no longer exist in the repo. See item 1 under "Decisions made" below for the full record.

## rtpengine

- **Purpose today**: anchors RTP between `net1` (SBC-facing) and `eth0` (cluster-facing), and gives Kamailio's `rtpengine_manage()` calls somewhere to rewrite SDP so FreeSWITCH's pod IP never appears in it.
- **Challenge it solves**: as the addendum argues, this duplicates the SBC's own job once FreeSWITCH can anchor media itself. Under today's design, though, before FreeSWITCH has `net1`, rtpengine is the only thing stopping FreeSWITCH's pod IP from leaking into SDP. `config/kamailio/kamailio.cfg.tmpl:107-109` says this explicitly in its own comment, and every `local/demo-call*.pcap` capture confirms FreeSWITCH's pod IP appears only in SIP headers, not in SDP, because of it.
- **What breaks without it (today's design)**: FreeSWITCH's pod-network IP leaks straight into SDP, the exact failure this project exists to prevent.
- **What breaks without it (addendum's design, FreeSWITCH on `net1`)**: nothing. SDP already carries FreeSWITCH's own voice-VLAN IP, so rtpengine has no SDP left to rewrite for the primary call path.
- **Alternatives**: SBC-direct to FreeSWITCH, the addendum's proposal. Routable pod IPs via Calico BGP, a bigger architecture change not evaluated here and already deferred.
- **Cost, as actually built, corrected against the addendum's premise**: one replica today, with no `strategy:` set either, the same SPOF-on-rollout risk as Kamailio and not yet flagged anywhere else. The addendum states that rtpengine's in-kernel forwarding "needs a host kernel module and extra privileges." That isn't true of what's deployed here. `config/rtpengine/entrypoint.sh` runs plain userspace forwarding, with no `--table` and no kernel module setup, and `charts/rtpengine/templates/deployment.yaml` sets no `securityContext`, no added capabilities, and no privileged flag. That cost is real only if in-kernel offload gets adopted later, not for the code in this repo today.
- **Verdict**: Conditional. Required under the current design, before the addendum. The addendum's own plan to disable it by default once FreeSWITCH owns `net1` is consistent with what's in the repo, not contradicted by it.
- **Evidence**: `config/kamailio/kamailio.cfg.tmpl:107-109`. `local/demo-call3.pcap`, and 4 and 5: SDP never carries `10.244.*`. `config/rtpengine/entrypoint.sh`. `charts/rtpengine/templates/deployment.yaml`.

## Kamailio `topos` and its state store

- **Purpose**: hide every internal address from the SBC, so the Record-Route, Via, and Contact headers it sees only ever carry Kamailio's own address.
- **Challenge it solves**: acceptance criterion 3 of this addendum, "address, interface, and DSCP checks all pass," meaning the SIP headers the SBC sees must contain only Kamailio's IP.
- **What breaks without it, verified, not reasoned**: running `strings local/demo-call3.pcap | grep '10\.244\.'`, and the same for `demo-call4.pcap`, `demo-call5.pcap`, and `rtp-debug-sbc.pcap`, shows pod-network IPs already present in SIP headers today:
  - `Record-Route: <sip:10.244.1.31;...>` is Kamailio's own `eth0` pod IP, doubled into a second Record-Route because `kamailio.cfg.tmpl`'s `force_send_socket()` makes the inbound and outbound socket differ, which the `rr` module reacts to by default.
  - `Contact: <sip:9199@10.244.3.35:5060;...>` and a matching `Via:` header both carry FreeSWITCH's pod IP. This doesn't depend on the media-path change at all, because FreeSWITCH's `sip-ip` stays on `eth0` either way (`config/freeswitch/internal.xml:102`).
  - `local/check-addresses.sh:17` only extracts `sip.msg_body`, the SDP, from the capture. It never extracts the headers above, so this leak passes the project's own existing check today. That's a real test gap in the current repo, independent of this addendum.
- **Alternatives**: hand-rewrite headers directly in `kamailio.cfg`, which reimplements what `topos` already does with more code and more places to get it wrong. Do nothing, which fails AC3, backed by the evidence above, not speculation.
- **Cost**: a state store, a database or Redis, to hold topology-hiding mappings across `kamailio-a`, `kamailio-b`, and restarts.
- **Verdict**: Required, independent of the media-path decision. This is a pre-existing gap the addendum happens to also need closed.
- **Evidence**: the pcap greps above. `local/check-addresses.sh:17`. `config/kamailio/kamailio.cfg.tmpl`'s `force_send_socket()` usage.

## The operator, CRDs, and mutating webhook (as planned in Phase 6)

- **Purpose as planned** (`dev-docs/architecture.md` build-order table): "Kamailio pod restarts keep its static IP; membership stays in sync with no sidecar."
- **What was actually built** (`config/dispatcher-operator`): a plain `controller-runtime` manager with one `Reconciler` watching `EndpointSlice` objects. No `CustomResourceDefinition` and no admission or mutating webhook exist anywhere in the repo. Confirmed by grep, not inferred from naming.
- **What breaks without the CRD and webhook portion**: nothing. The plan credited `dispatcher-operator` with keeping the static IP across restarts, but the Multus `ips` annotation on the Kamailio Deployment (`charts/kamailio/templates/deployment.yaml:18`) does that already, with nothing to do with `dispatcher-operator`.
- **Alternatives**: build the CRD and webhook as originally planned. No identified need now that the annotation already covers the stated goal.
- **Cost of building it anyway**: an admission webhook needs a cert, a `ValidatingWebhookConfiguration` or a `MutatingWebhookConfiguration`, and a TLS-serving endpoint. That's meaningful operational overhead for a goal that's already met.
- **Verdict**: Remove from the plan. What was actually built, the `dispatcher-operator` reconciler, stands on its own merits under the dispatcher sync section above. The CRD and webhook portion was never built, and nothing currently needs it.
- **Evidence**: `grep -rni "mutatingwebhook\|crd\b"` across the whole repo returns nothing. `charts/kamailio/templates/deployment.yaml:18`.

## KEDA or custom-metrics autoscaling

- **Purpose as planned**: autoscale FreeSWITCH on active channels or CPS instead of CPU (principle 7, Phase 7).
- **What's actually in the repo**: nothing. No KEDA manifests, no custom-metrics adapter, and no `HorizontalPodAutoscaler` object exists anywhere (grep empty).
- **What breaks without it**: nothing, against this addendum's acceptance criteria. AC4, "scale and rolling update during load: zero dropped calls," can be exercised with a manual `kubectl scale` during a SIPp load run. The criterion is about not dropping calls during scaling, not about scaling being automatic.
- **Alternatives**: manual scaling, adequate for a first release. Plain CPU-based HPA, explicitly rejected by principle 7, since CPU is a stated poor proxy for call load.
- **Cost**: KEDA is a second autoscaling control plane, and it would need a channel-count metrics source that doesn't exist yet either.
- **Verdict**: Nice to have. Defer.
- **Evidence**: a grep for "keda" across the repo returns nothing. AC4's wording doesn't require automation.

## OpenTelemetry, Prometheus, Grafana

- **Purpose as planned**: per-component metrics and SIP and RTP quality tracing (principle 8, Phase 7).
- **What's actually built**: `dispatcher-operator` links `prometheus/client_golang` and exposes three counters and gauges on its own metrics endpoint (`charts/dispatcher-operator/templates/metrics-service.yaml`). That's the entire footprint. No Prometheus server, no `ServiceMonitor`, no Grafana dashboard, and no OpenTelemetry collector or instrumentation exists anywhere else (grep empty).
- **What breaks without the rest**: nothing acceptance-critical in this addendum. AC5, "`make quality` shows jitter, loss, and MOS," is satisfied by reading FreeSWITCH's own call variables into `quality.json`, which needs none of Prometheus, Grafana, or OpenTelemetry.
- **Alternatives**: keep the one metrics endpoint already written, which is free. Stand up a real Prometheus and Grafana stack only once there's an actual dashboard consumer for it.
- **Cost**: a Prometheus server, scrape config, Grafana, and dashboards-as-code add real ongoing operational overhead for a project with one environment running so far.
- **Verdict**: keep what's built, the `dispatcher-operator`'s metrics endpoint, already written and cheap. The rest is Nice to have. Defer.
- **Evidence**: a grep for Prometheus, Grafana, and OpenTelemetry outside `dispatcher-operator`'s own code returns nothing. AC5 doesn't require any of it.

## PodDisruptionBudgets, drain scripts, long termination grace periods

These are three different things, each with a different state in the repo. This section covers each one separately.

- **Drain script: built and verified.** `config/freeswitch/drain.sh` is a real `preStop` hook. It marks the pod inactive (`dispatcher.set_state`) in both Kamailio instances by RPC, then polls `fs_cli "show calls count"` until the count hits 0 or `DRAIN_TIMEOUT` expires. This directly serves AC4. Verdict: Required. Evidence: the script itself, wired in via `charts/freeswitch/templates/deployment.yaml:28-31`.
- **Termination grace period: built, but its value needs re-deriving for production.** The drain script's own comment says `terminationGracePeriodSeconds` must exceed `DRAIN_TIMEOUT`, or Kubernetes kills the pod anyway. Locally it's 60s against a 50s drain timeout. Both are lab-sized placeholders per `charts/freeswitch/values.yaml`'s own comment, not production values. Verdict: Required. The value needs setting from the real maximum call duration FreeSWITCH is told to drain for, not copied from the lab.
- **PodDisruptionBudget: not built.** No `PodDisruptionBudget` object exists for Kamailio, rtpengine, or FreeSWITCH (grep empty). What breaks without one: a voluntary eviction, such as a node drain for RKE2 upgrades or a cluster-autoscaler scale-down, can take out more FreeSWITCH pods at once, or evict whichever Kamailio instance happens to be touched, with none of the "no more than N at a time" protection a PodDisruptionBudget gives. That's a different failure path than the Deployment's own `maxUnavailable`, which doesn't apply to node-drain-initiated eviction at all. Verdict: Required once this runs on real nodes that get drained for maintenance. Nice to have for the kind lab, which doesn't drain nodes today.
- **Evidence**: `config/freeswitch/drain.sh`. `charts/freeswitch/values.yaml:4-8`. A grep for "poddisruptionbudget" across the repo is empty.

## Node taints and labels, separate edge vs media pools

- **Purpose**: keep voice-VLAN-attached pods off nodes without the VLAN NIC, and vice versa.
- **What's built today**: a `voice-edge=true:NoSchedule` taint and a `voice-role: edge` label, consumed by Kamailio's and rtpengine's `nodeSelector` and `tolerations`. FreeSWITCH already uses a *different* `nodeSelector: voice-role: media` (`charts/freeswitch/templates/deployment.yaml:17-18`), with no matching taint or toleration anywhere. It's already on a separate labeled pool today, just not one reserved by a taint.
- **Challenge it solves**: once FreeSWITCH also needs the voice VLAN NIC, this addendum's own change, any node lacking that NIC simply can't run it. This stops being a scheduling nicety and becomes a hard requirement.
- **What breaks without it**: FreeSWITCH pods land on nodes with no `net1`, and the startup check the addendum proposes, verifying `net1` exists, would fail on every one of them.
- **Alternatives**: one shared pool for every node, which only works if every node in the cluster has the voice VLAN NIC. That defeats the point of a limited voice VLAN footprint.
- **Cost**: one more label and taint pair, plus renaming the media pool to the addendum's "voice nodes" concept.
- **Verdict**: Required, and becomes mandatory the moment FreeSWITCH needs `net1`.
- **Evidence**: `charts/freeswitch/templates/deployment.yaml:17-18`. `charts/kamailio/templates/deployment.yaml:20-24`. `charts/rtpengine/templates/deployment.yaml:18-23`. `dev-docs/prerequisites.md`'s edge node count checklist item.

## Canal as primary CNI vs Calico BGP

- **Purpose**: general pod networking.
- **Challenge it raises**: could pod IPs become routable on the voice VLAN via Calico BGP, removing Multus, Whereabouts, and rtpengine entirely?
- **What breaks without evaluating this now**: nothing, against any current acceptance criterion. Once FreeSWITCH owns a `net1` IP directly, this addendum's own change, no voice traffic crosses the pod network at all, so the CNI choice stops mattering to the call path either way.
- **Alternatives**: Canal, the current RKE2 default, needs no action. Calico BGP means exposing the whole pod CIDR to the voice VLAN's router, a materially different trust boundary the network team hasn't signed off on.
- **Cost**: `dev-docs/prerequisites.md:27` already flags a CNI migration on an existing cluster as "a separate, riskier change." `dev-docs/architecture.md:103` already deferred this exact question to its own ADR.
- **Verdict**: Remove from this addendum's scope. Already deferred, and now also moot for the reason above.
- **Evidence**: `dev-docs/architecture.md:103`. `dev-docs/prerequisites.md:27`. The addendum's own media-path change removes the dependency on this question.

## MetalLB or any load balancer

- **Purpose it would serve**: none found.
- **What's in the repo**: no mention of MetalLB anywhere (grep empty). Every Service in every chart is `ClusterIP` or headless (`clusterIP: None`). None are `LoadBalancer` typed.
- **What breaks without it**: nothing. It isn't used.
- **Alternatives**: none.
- **Cost**: none.
- **Verdict**: Remove. Not present, and the architecture doc's own "why standard Kubernetes patterns don't work here" section explicitly argues against an L4 load balancer for SIP, because it breaks server-initiated SIP methods such as re-INVITE, BYE, and OPTIONS.
- **Evidence**: grep empty. `dev-docs/architecture.md`'s L4 LB bullet. Every `charts/*/templates/*service*.yaml` checked.

## Defects found along the way (not components, worth recording now)

These surfaced while gathering evidence above. They aren't fixed here, following the addendum's "do Step 1 first, stop" instruction.

1. **Kamailio's rollout can collide on its own static IP.** No `strategy:` is set on `charts/kamailio/templates/deployment.yaml`. A default `RollingUpdate` will try to run two pods against the same `voice-static` NAD IP during any update. This needs `strategy.type: Recreate`.
2. **`local/check-addresses.sh` checks only SDP, not SIP headers.** It extracts `sip.msg_body` only, so the Record-Route, Contact, and Via leaks documented above pass today's check silently. The addendum's Step 4 "address check" needs to explicitly cover headers, not assume the existing script already does.
3. **FreeSWITCH has no readiness probe.** Both `dispatcher-sync` (`sync.py:48`, which defaults `ready` to `True` when absent) and `dispatcher-operator` (`freeswitch_reconciler.go:96`, with the same default) treat a missing `ready` condition as ready. A new FreeSWITCH pod can get added to the dispatcher before SIP is actually listening. This is already a known gap noted in `dev-docs/architecture.md:96-97`, repeated here because it threatens this addendum's AC4 just as much as the original phase's acceptance criteria.
4. **rtpengine has the same single-instance rollout exposure as Kamailio**, no `strategy:`, `replicas: 1`, just without a static IP to collide over. Lower severity, same shape.

## Summary

**Minimum viable set that still meets every acceptance criterion in this addendum:**
Multus, Whereabouts, one of ipvlan or macvlan (environment-decided), static IPAM NAD, both Kamailio instances with `topos` and a state store, one dispatcher sync mechanism (`dispatcher-operator` or `dispatcher-sync`, not both), FreeSWITCH with `net1` plus its existing drain script and a correctly sized grace period, node taints and labels covering both the edge and voice-node pools, the rtpengine chart kept but `enabled: false` by default, and the address, interface, DSCP, and quality tooling from Step 4 (new work).

**What to cut for a first production release:**
KEDA and custom-metrics autoscaling, a real Prometheus server plus Grafana plus OpenTelemetry (keep the one metrics endpoint already written), the Calico BGP question (moot once FreeSWITCH has `net1`), and PodDisruptionBudgets, which can wait specifically for the kind lab but not for the first real-node rollout.

**Pure nice-to-have, already in the repo, defensible to call out bluntly:**
one of the two dispatcher sync implementations is redundant with the other and should be removed. The CRD and mutating-webhook portion of the Phase 6 plan was never built, and nothing needs it. MetalLB was never present and isn't needed.

## Decisions made (updated after this review)

1. **Dispatcher sync**: `dispatcher-sync` (Python) kept. `dispatcher-operator` (Go) removed. This reverses this document's original recommendation, per the user's explicit choice. `charts/dispatcher-operator` and `config/dispatcher-operator` are deleted.
2. **Path**: `dev-docs/adr/` confirmed as the right location, matching `dev-docs/architecture.md:101`'s existing convention. This file's numbering stands.
3. **rtpengine**: removed entirely, not just disabled by default. See [ADR 002](002-remove-rtpengine.md).
4. **topos**: confirmed required and proposed for implementation, but not yet added. The state-store choice is written up in [ADR 003](003-topos-state-store.md), awaiting a decision before the `topos` module itself gets wired into `kamailio.cfg.tmpl`.
5. **Canal vs Calico BGP**: left as a deferred, lower-priority ADR in `dev-docs/architecture.md`. Moot for the call path now that FreeSWITCH owns `net1` directly either way.
