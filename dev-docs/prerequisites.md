# Prerequisites (Phase 0)

What the network and platform teams must provide before Phase 1 (real RKE2) starts. Phases L through the end of Phase 0 need none of this — the local lab simulates it. This checklist is for sign-off, not for filling in values yet; real IPs/VLANs/CIDRs go into `environments/lab/values.yaml` only once provided.

## Network

- [ ] Voice VLAN trunked to a second NIC on every voice node (the NIC `net1` attaches to). Kamailio and FreeSWITCH both need it now — FreeSWITCH anchors media with the SBC directly (see [ADR 002](adr/002-remove-rtpengine.md)).
- [ ] Confirm whether the switch ports voice nodes connect to allow multiple MAC addresses per port.
  - Yes → ipvlan L2 is usable (default).
  - No, or unknown → macvlan is required instead; flag this explicitly, don't assume.
- [ ] Routes: which subnets (SBC signaling, SBC media) must egress via `net1` rather than the default route. Needed in every NetworkAttachmentDefinition's `routes` field — without them, replies leave via `eth0` and get NATed. This now matters for FreeSWITCH's pool, not just Kamailio's static IPs.
- [ ] SBC-side: confirm the SBC can send/receive media to/from a *pool* of FreeSWITCH IPs (not one fixed relay address), and allow media to and from the FreeSWITCH pool range (media realm or remote media subnet config).
- [ ] Firewall rules on the voice VLAN:
  - SIP ports → Kamailio static IPs only.
  - RTP port range → the FreeSWITCH pool only, on FreeSWITCH's RTP port range.
  - Both restricted to SBC source addresses.
  - Note: Kubernetes `NetworkPolicy` does not apply to `net1` traffic — these rules must exist at the network/switch/firewall layer.

## IP plan

- [ ] Static IP for each Kamailio instance (one per planned replica, A/B at minimum).
- [ ] Pool range for FreeSWITCH's `net1` (sized for max replicas + rollout surge + headroom, not just current headcount — see `charts/voice-network/values.yaml`'s `freeswitch.maxReplicas`/`maxSurge`/`poolHeadroom`).
- [ ] Confirmation these ranges don't collide with DHCP or other static assignments on the VLAN.

## Platform

- [ ] Voice node count and which physical nodes they are (these get the `voice-node=true:NoSchedule` taint; both Kamailio and FreeSWITCH schedule there now).
- [ ] RKE2 version in use or planned, and whether this is a fresh cluster or an existing one (CNI migration to Multus+Canal is a separate, riskier change if existing).
- [ ] Container registry reachable from the cluster (public registry vs. internal mirror) for pinned image tags.

## Sign-off

| Reviewed by | Date | Notes |
|---|---|---|
| | | |
