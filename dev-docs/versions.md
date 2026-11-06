# Pinned versions

| Component | Version | Reference |
|---|---|---|
| kind | v0.33.0, as installed | [releases](https://github.com/kubernetes-sigs/kind/releases/tag/v0.33.0) |
| kind node image | `kindest/node:v1.37.0@sha256:a1ed56cfb0e7b93589bdf97c8cd566405a265939e3620fc4f5de89adff580ae5` | kind v0.33.0 release notes |
| Multus CNI (thick plugin) | v4.3.1 | [releases](https://github.com/k8snetworkplumbingwg/multus-cni/releases/tag/v4.3.1). Manifest `deployments/multus-daemonset-thick.yml` |
| Whereabouts | v0.9.4 | [releases](https://github.com/k8snetworkplumbingwg/whereabouts/releases/tag/v0.9.4). Installed per `README.md` (`doc/crds/*.yaml`) |
| containernetworking/plugins (macvlan, ipvlan, static) | v1.9.1 | [releases](https://github.com/containernetworking/plugins/releases/tag/v1.9.1). Copied onto each voice node by `local/addons.sh`. kind's node image ships only the CNI binaries kindnet itself needs |
| SIPp | v3.8.0 | [releases](https://github.com/SIPp/sipp/releases/tag/v3.8.0). Built from source per `docker/Dockerfile.debian` |
| Container runtime, local lab only | Podman 6.1.4, machine, netavark backend | Docker isn't installed on the development machine. See `dev-docs/local-lab.md` |
| podman-compose, local lab only | 1.6.0 | Brings up `local/compose.yaml` (voice network and SBC). `dev-docs/local-lab.md` covers the `podman compose` incompatibility this avoids |
| Kamailio | 6.0.x from `deb.kamailio.org`'s `kamailio60` repo, bookworm. Installed version is 6.0.8+bpo12, not pinned exactly | `config/kamailio/Dockerfile`. Needed for `topos_htable`, added in Kamailio 6.0 (Dec 2024). See ADR 003. Debian bookworm's own package is 5.6.x |
| FreeSWITCH base image | `ghcr.io/patrickbaus/freeswitch-docker:1.11.3` | `config/freeswitch/Dockerfile` |

A `controller-runtime` Go operator, `dispatcher-operator`, was evaluated as an alternative to `dispatcher-sync` and removed. See ADR 001's necessity review. `dispatcher-sync` is the kept implementation.
