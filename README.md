# multus-sip-stack

multus-sip-stack is a SIP voice stack built from Kamailio and FreeSWITCH, running on ordinary Kubernetes with no `hostNetwork` anywhere. Standard Kubernetes networking, such as Services, kube-proxy, and overlay NAT, cannot carry a SIP and RTP call without breaking it. This project uses Multus instead: it gives the voice-node tier (Kamailio and FreeSWITCH) a real address on the voice network. Every pod still stays a normal, schedulable, unprivileged workload. The SBC anchors media directly with FreeSWITCH, so no media relay runs in the cluster. See [ADR 002](dev-docs/adr/002-remove-rtpengine.md) for the reasoning.

[Interactive runtime architecture diagram](https://iamashraf.github.io/multus-sip-stack/runtime-architecture.html), explorable in a browser: the call path, the trust boundaries, and how membership and drain work.

## How a call flows through the stack

A call enters through an SBC, which hands SIP signaling to one of two Kamailio instances. Kamailio dispatches the call to a FreeSWITCH pod. The SBC and that FreeSWITCH pod then exchange RTP directly over the voice VLAN, because FreeSWITCH owns its own voice-VLAN address (`net1`). No pod-network IP ever reaches the SBC, and nothing in the cluster relays media.

| Component | Role | Network |
|---|---|---|
| SBC | Session border controller (SIPp stand-in locally) | Voice VLAN |
| Kamailio (`kamailio-a`, `kamailio-b`) | SIP dispatcher, static IP | Voice VLAN and pod network |
| FreeSWITCH | Media server, anchors RTP with the SBC directly | Voice VLAN and pod network |
| dispatcher-sync | Polls FreeSWITCH's EndpointSlice, keeps Kamailio's dispatcher list in sync | Pod network only |

## Concepts

| Term | What it is | Learn more |
|---|---|---|
| SBC | The signaling and media boundary a carrier exposes to the outside world | [Wikipedia](https://en.wikipedia.org/wiki/Session_border_controller) |
| SIP | The protocol that sets up, modifies, and tears down a call | [RFC 3261](https://www.rfc-editor.org/rfc/rfc3261) |
| SDP | A description of a media session (codecs, IPs, ports) inside SIP's offer and answer exchange | [RFC 8866](https://www.rfc-editor.org/rfc/rfc8866) |
| RTP | The protocol that carries the actual audio once a call is set up | [RFC 3550](https://www.rfc-editor.org/rfc/rfc3550) |
| Voice VLAN | A network segment that carries only SIP and RTP traffic, isolated from everything else | [Wikipedia (802.1Q)](https://en.wikipedia.org/wiki/IEEE_802.1Q) |
| Multus | A Kubernetes CNI meta-plugin that lets one pod attach more than one network interface | [multus-cni](https://github.com/k8snetworkplumbingwg/multus-cni) |
| NetworkAttachmentDefinition (NAD) | The CRD that tells Multus how to wire up an extra interface | [how-to-use.md](https://github.com/k8snetworkplumbingwg/multus-cni/blob/master/docs/how-to-use.md) |
| ipvlan and macvlan | Linux virtual interfaces this project's NADs use to put a pod on the voice VLAN without a full virtual switch | [CNI plugins](https://www.cni.dev/plugins/current/main/ipvlan/) |
| Whereabouts | The IPAM plugin that hands out voice-VLAN IPs cluster-wide without clashes | [whereabouts](https://github.com/k8snetworkplumbingwg/whereabouts) |
| EndpointSlice | Kubernetes' own live list of which pod IPs back a Service | [Kubernetes docs](https://kubernetes.io/docs/concepts/services-networking/endpoint-slices/) |
| hostNetwork | A pod mode that shares the host's network namespace instead of getting its own | [Kubernetes docs](https://kubernetes.io/docs/concepts/cluster-administration/networking/) |
| Kamailio | The open-source SIP server acting as the dispatcher here | [kamailio.org](https://www.kamailio.org/w/) |
| FreeSWITCH | The open-source media server that answers calls and plays audio | [freeswitch.org](https://freeswitch.org/) |

## Design principles

The stack holds to four rules:

- No `hostNetwork` and no privileged pods, unless an exception is called out explicitly.
- A pod owns its address: the IP in SDP and the source IP of every packet match, in both directions.
- Dispatcher membership comes from Kubernetes EndpointSlices, never a hand-edited list.
- A call in progress blocks termination: nothing holding an active call terminates until the call ends or a bounded timeout passes.

See [dev-docs/architecture.md](dev-docs/architecture.md) for the full design, the build order, and deferred ADRs.

## Run the local lab

The local lab is a kind cluster that reproduces the production network shape on one laptop. It lets you build and test every phase before you touch real infrastructure. The same Helm charts deploy here and in production. Only `environments/local/values.yaml` differs.

Three `make` targets cover the whole lab:

```
cd local
make up       # kind cluster, voice network + sbc container, Multus + Whereabouts
make test     # harness self-test: SIPp loopback inside the sbc container
make down     # full teardown
```

See [dev-docs/local-lab.md](dev-docs/local-lab.md) for the node layout, Podman-on-macOS notes, and known differences from production.

## Verify the call path

Three scripts prove a call carries audio, not just signaling:

- `test/sipp/media-flow-check.sh [kamailio-net1-ip]` plays a real recording through the stack and confirms RTP reaches FreeSWITCH's voice-VLAN address directly. A successful SIP call alone doesn't prove that RTP reached FreeSWITCH.
- `local/check-addresses.sh <pcap>` fails if any pod-network IP leaked into SIP or SDP. The capture must come from a real endpoint, not a bystander container, which sees only broadcast traffic on the bridge.
- `local/capture.sh [iface]` takes that capture from inside the sbc container.

See [dev-docs/verification.md](dev-docs/verification.md) for the full end-to-end call flow, an interactive sequence diagram, and the exact commands used to decode and confirm real audio.

## Repo layout

The repo has these top-level directories:

- `charts/` holds one Helm chart per deployable piece: `dispatcher-sync`, `freeswitch`, `kamailio`, and `voice-network`.
- `config/` holds the build sources (Dockerfiles, entrypoints, app config) that the charts deploy.
- `environments/` holds one `values.yaml` per environment. Only `local` has one today. `lab` is reserved for RKE2.
- `local/` is the kind-based local lab (compose network, SIPp SBC, helper scripts).
- `test/` holds verification scripts and manifests.
- `dev-docs/` holds the architecture, local-lab, prerequisites, verification, and versions docs, plus ADRs under `adr/`.
- `docs/` holds the published interactive diagrams: `runtime-architecture.html`, linked above, and `e2e-call-verification.html`, linked from `dev-docs/verification.md`.
