# Prerequisites (Phase 0)

The network and platform teams must provide everything on this checklist before Phase 1 (real RKE2) starts. Phase 0 and Phase L need none of it. The local lab simulates it instead. Use this checklist for sign-off, not to fill in values: real IPs, VLANs, and CIDRs go into `environments/lab/values.yaml` only once the teams provide them.

## Network

- [ ] Confirm the voice VLAN is trunked to a second NIC on every voice node. This is the NIC `net1` attaches to. Both Kamailio and FreeSWITCH need it, because FreeSWITCH anchors media with the SBC directly (see [ADR 002](adr/002-remove-rtpengine.md)).
- [ ] Confirm whether the switch ports the voice nodes connect to allow multiple MAC addresses per port.
  - If yes, use ipvlan L2 (the default).
  - If no, or unknown, use macvlan instead. Flag this explicitly. Don't assume.
- [ ] Confirm which subnets (SBC signaling, SBC media) must egress via `net1` instead of the default route. Every NetworkAttachmentDefinition needs these subnets in its `routes` field. Without them, replies leave via `eth0` and get NATed. This matters for FreeSWITCH's pool as well as Kamailio's static IPs.
- [ ] Confirm the SBC can send and receive media to and from a pool of FreeSWITCH IPs, not one fixed relay address, and that it allows media to and from the FreeSWITCH pool range (media realm or remote media subnet config).
- [ ] Confirm firewall rules exist on the voice VLAN:
  - SIP ports reach Kamailio's static IPs only.
  - The RTP port range reaches the FreeSWITCH pool only, on FreeSWITCH's RTP port range.
  - Both rules restrict traffic to SBC source addresses.
  - Note: Kubernetes `NetworkPolicy` doesn't apply to `net1` traffic. These rules must exist at the network, switch, or firewall layer instead.

## IP plan

- [ ] Confirm a static IP for each Kamailio instance (one per planned replica, `kamailio-a` and `kamailio-b` at minimum).
- [ ] Confirm a pool range for FreeSWITCH's `net1`, sized for max replicas plus rollout surge plus headroom, not just the current pod count. See `freeswitch.maxReplicas`, `freeswitch.maxSurge`, and `freeswitch.poolHeadroom` in `charts/voice-network/values.yaml`.
- [ ] Confirm these ranges don't collide with DHCP or other static assignments on the VLAN.

## Platform

- [ ] Confirm the voice node count and which physical nodes they are. These nodes get the `voice-node=true:NoSchedule` taint. Both Kamailio and FreeSWITCH schedule on them.
- [ ] Confirm the RKE2 version in use or planned, and whether this is a fresh cluster or an existing one. Migrating an existing cluster's CNI to Multus and Canal is a separate, riskier change than setting one up fresh.
- [ ] Confirm a container registry is reachable from the cluster, public or an internal mirror, for the pinned image tags.

## Sign-off

| Reviewed by | Date | Notes |
|---|---|---|
| | | |
