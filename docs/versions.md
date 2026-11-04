# Pinned versions

| Component | Version | Reference |
|---|---|---|
| kind | v0.33.0 (as installed) | [releases](https://github.com/kubernetes-sigs/kind/releases/tag/v0.33.0) |
| kind node image | `kindest/node:v1.37.0@sha256:a1ed56cfb0e7b93589bdf97c8cd566405a265939e3620fc4f5de89adff580ae5` | kind v0.33.0 release notes |
| Multus CNI (thick plugin) | v4.3.1 | [releases](https://github.com/k8snetworkplumbingwg/multus-cni/releases/tag/v4.3.1), manifest `deployments/multus-daemonset-thick.yml` |
| Whereabouts | v0.9.4 | [releases](https://github.com/k8snetworkplumbingwg/whereabouts/releases/tag/v0.9.4), install per `README.md` (`doc/crds/*.yaml`) |
| SIPp | v3.8.0 | [releases](https://github.com/SIPp/sipp/releases/tag/v3.8.0), built from source per `docker/Dockerfile.debian` |
| Container runtime (local only) | Podman 6.1.4 (machine, netavark backend) | n/a — Docker not installed on this machine; see `docs/local-lab.md` |
| podman-compose (local only) | 1.6.0 | brings up `local/compose.yaml` (voice network + sbc); see `docs/local-lab.md` for the `podman compose` incompatibility this avoids |

Kamailio, rtpengine, and FreeSWITCH image tags are pinned in Phases 2-4 and added to this table then.
