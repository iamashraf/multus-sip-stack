# Prerequisites (Phase 0)

What the network and platform teams must provide before Phase 1 (real RKE2) starts. Phases L through the end of Phase 0 need none of this — the local lab simulates it. This checklist is for sign-off, not for filling in values yet; real IPs/VLANs/CIDRs go into `environments/lab/values.yaml` only once provided.

## Network

- [ ] Voice VLAN trunked to a second NIC on every edge node (the NIC `net1` attaches to).
- [ ] Confirm whether the switch ports edge nodes connect to allow multiple MAC addresses per port.
  - Yes → ipvlan L2 is usable (default).
  - No, or unknown → macvlan is required instead; flag this explicitly, don't assume.
- [ ] Routes: which subnets (SBC signaling, SBC media) must egress via `net1` rather than the default route. Needed in every NetworkAttachmentDefinition's `routes` field — without them, replies leave via `eth0` and get NATed.
- [ ] Firewall rules on the voice VLAN:
  - SIP ports → Kamailio static IPs only.
  - RTP port range → the rtpengine pool only.
  - Both restricted to SBC source addresses.
  - Note: Kubernetes `NetworkPolicy` does not apply to `net1` traffic — these rules must exist at the network/switch/firewall layer.

## IP plan

- [ ] Static IP for each Kamailio instance (one per planned replica, A/B at minimum).
- [ ] Pool range for rtpengine (sized for peak concurrent sessions, not just current headcount).
- [ ] Confirmation these ranges don't collide with DHCP or other static assignments on the VLAN.

## Platform

- [ ] Edge node count and which physical nodes they are (these get the `voice-edge=true:NoSchedule` taint).
- [ ] RKE2 version in use or planned, and whether this is a fresh cluster or an existing one (CNI migration to Multus+Canal is a separate, riskier change if existing).
- [ ] Container registry reachable from the cluster (public registry vs. internal mirror) for pinned image tags.

## Sign-off

| Reviewed by | Date | Notes |
|---|---|---|
| | | |
