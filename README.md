# multus-sip-stack

A SIP voice stack (Kamailio + rtpengine + FreeSWITCH) that runs on ordinary Kubernetes, with no `hostNetwork` anywhere. Standard k8s networking (Services, kube-proxy, overlay NAT) can't carry a SIP/RTP call without breaking it, so this project uses Multus to give only the edge tier (Kamailio, rtpengine) a real address on the voice network, while every pod stays a normal, schedulable, unprivileged workload.

[Interactive runtime architecture diagram](https://iamashraf.github.io/multus-sip-stack/runtime-architecture.html) — the call path, the trust boundaries, and how membership and drain actually work, explorable in a browser.

## How it works

A call enters through an SBC, which hands SIP signaling to one of two Kamailio instances and media to rtpengine. Kamailio dispatches the call to a FreeSWITCH pod; rtpengine anchors the RTP media between the SBC's voice VLAN and the cluster's pod network, so no pod-network IP ever reaches the SBC and no pod ever needs a routable VLAN address of its own.

| Component | Role | Network |
|---|---|---|
| SBC | Session border controller (SIPp stand-in locally) | Voice VLAN |
| Kamailio (A/B) | SIP dispatcher, static IP | Voice VLAN + pod network |
| rtpengine | Media relay, bridges the two networks | Voice VLAN + pod network |
| FreeSWITCH | Media server, ordinary pod pool | Pod network only |
| dispatcher-sync | Watches FreeSWITCH's EndpointSlice, keeps Kamailio's dispatcher list in sync | Pod network only |

## Concepts

| Term | What it is | Learn more |
|---|---|---|
| SBC | The signaling/media boundary a carrier exposes to the outside world | [Wikipedia](https://en.wikipedia.org/wiki/Session_border_controller) |
| SIP | The protocol that sets up, modifies, and tears down a call | [RFC 3261](https://www.rfc-editor.org/rfc/rfc3261) |
| SDP | Describes a media session (codecs, IPs, ports) inside SIP's offer/answer | [RFC 8866](https://www.rfc-editor.org/rfc/rfc8866) |
| RTP | The protocol that carries the actual audio once a call is set up | [RFC 3550](https://www.rfc-editor.org/rfc/rfc3550) |
| Voice VLAN | A network segment carrying only SIP/RTP traffic, isolated from everything else | [Wikipedia (802.1Q)](https://en.wikipedia.org/wiki/IEEE_802.1Q) |
| Multus | A Kubernetes CNI meta-plugin that lets one pod attach more than one network interface | [multus-cni](https://github.com/k8snetworkplumbingwg/multus-cni) |
| NetworkAttachmentDefinition (NAD) | The CRD that tells Multus how to wire up an extra interface | [how-to-use.md](https://github.com/k8snetworkplumbingwg/multus-cni/blob/master/docs/how-to-use.md) |
| ipvlan / macvlan | Linux virtual interfaces this project's NADs use to put a pod on the voice VLAN without a full virtual switch | [CNI plugins](https://www.cni.dev/plugins/current/main/ipvlan/) |
| Whereabouts | The IPAM plugin that hands out voice-VLAN IPs cluster-wide without clashes | [whereabouts](https://github.com/k8snetworkplumbingwg/whereabouts) |
| EndpointSlice | Kubernetes' own live list of which pod IPs back a Service | [k8s docs](https://kubernetes.io/docs/concepts/services-networking/endpoint-slices/) |
| hostNetwork | A pod mode that shares the host's network namespace instead of getting its own | [k8s docs](https://kubernetes.io/docs/concepts/cluster-administration/networking/) |
| Kamailio | The open-source SIP server acting as the dispatcher here | [kamailio.org](https://www.kamailio.org/w/) |
| rtpengine | The open-source media relay that anchors RTP across the two networks | [sipwise/rtpengine](https://github.com/sipwise/rtpengine) |
| FreeSWITCH | The open-source media server that answers calls and plays audio | [freeswitch.org](https://freeswitch.org/) |

## Design principles

- No `hostNetwork`, no privileged pods, without an explicit exception.
- A pod owns its address: the IP in SDP and the source IP of every packet match, in both directions.
- Dispatcher membership derives from Kubernetes (EndpointSlices), never a hand-edited list.
- Calls are first-class lifecycle state: nothing terminates while holding active calls, up to a bounded timeout.

See [dev-docs/architecture.md](dev-docs/architecture.md) for the full design, the build order, and deferred ADRs.

## Quickstart (local lab)

The local lab is a kind cluster that reproduces the production network shape on one laptop, so every phase can be built and tested before touching real infrastructure. Same Helm charts deploy here and in production; only `environments/local/values.yaml` differs.

```
cd local
make up       # kind cluster, voice network + sbc container, Multus + Whereabouts
make test     # harness self-test: SIPp loopback inside the sbc container
make down     # full teardown
```

See [dev-docs/local-lab.md](dev-docs/local-lab.md) for the node layout, Podman-on-macOS notes, and known differences from production.

## Verification

- `test/sipp/media-flow-check.sh [kamailio-net1-ip]` plays a real recording through the whole stack and confirms RTP actually reaches rtpengine's voice-VLAN address — a successful SIP call alone doesn't prove that.
- `local/check-addresses.sh <pcap>` fails if any pod-network IP leaked into SIP or SDP, checked against a capture taken from a real endpoint (not a bystander container, which only sees broadcast traffic on the bridge).
- `local/capture.sh [iface]` takes that capture from inside the sbc container.

## Repo layout

- `charts/` — Helm charts, one per component
- `config/` — build sources (Dockerfiles, entrypoints, app config) that the charts deploy
- `environments/` — one `values.yaml` per environment (`local`, with `lab` reserved for RKE2)
- `local/` — the kind-based local lab (compose network, SIPp SBC, helper scripts)
- `test/` — verification scripts and manifests
- `dev-docs/` — detailed architecture, local-lab notes, prerequisites, and ADRs
