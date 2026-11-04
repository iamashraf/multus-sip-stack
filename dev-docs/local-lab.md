# Local lab

A kind cluster on one laptop that reproduces the production network shape, so Phases 1-5 can be built and tested before anything touches real RKE2. Same Helm charts deploy here and on RKE2; only `environments/local/values.yaml` differs.

## Layout

| Node role | What it is |
|---|---|
| control-plane | kind control plane |
| edge x2 (`voice-role=edge`, tainted `voice-edge=true:NoSchedule`) | attached to the `voice` network via a second interface, same as production edge nodes |
| media x1 (`voice-role=media`) | plain pod network only, same as production FreeSWITCH nodes |

`compose.yaml` declares the `voice` network and the `sbc` container (a SIPp-based stand-in for the real SBC, on a fixed IP) as one unit - `podman-compose up`/`down` brings up or tears down both together. kind owns its own node containers, so they can't be compose services; `voice-net.sh` is what attaches the two kind-managed edge workers to that same network afterward.

## Known differences from production

- **kind instead of RKE2; kindnet instead of Canal** as the primary CNI. Multus runs alongside it the same way it will on RKE2.
- **A podman bridge network instead of a real VLAN**, so there's no switch MAC-per-port limit to work around. `environments/local/values.yaml` sets `voiceNetwork.attachmentType: macvlan` as the local default; set it to `ipvlan` to test that path too.
- **No real SBC behavior.** The fake SBC (SIPp) sends and receives calls; it does no topology hiding or header manipulation the way a production SBC would.

## Docker vs. Podman

This machine has Podman, not Docker. `compose.yaml` is brought up with `podman-compose` (not `podman compose`, which shells out to the real `docker-compose` binary and hits an image-commit/policy incompatibility on this machine - use `podman-compose` directly), `kind` is told to use Podman via `KIND_EXPERIMENTAL_PROVIDER=podman` (set in the `Makefile`), and `voice-net.sh` uses plain `podman network connect`. Nothing about the network model changes, only the CLI.

Two Podman-on-macOS gotchas hit while building this, in case they resurface:

- **Bind mounts only work under `$HOME`.** Podman's machine VM (via virtiofs) shares `$HOME` but not `/tmp`. `capture.sh`/`check-addresses.sh` default their output under the current directory for this reason - don't redirect them to `/tmp`. Also pass `-v` an absolute path, never a relative one (`.` silently becomes an empty named volume instead of a bind mount).
- **`policy.json` must allow the `containers-storage` transport.** If `podman build`/`podman-compose build` fails with `Source image rejected ... containers-storage:[...] is rejected by policy`, the machine's `~/.config/containers/policy.json` has no exception for locally-built images. Add one (`"containers-storage": {"": [{"type": "insecureAcceptAnything"}]}` under `transports`) - this only affects images already in local storage, not remote pulls.

## Usage

```
cd local
make up       # kind cluster, voice network + sbc container (compose.yaml), Multus + Whereabouts
make test     # harness self-test: SIPp loopback inside the sbc container (see Makefile comment - this doesn't touch Kamailio/rtpengine/FreeSWITCH yet)
make capture IFACE=eth0                          # tcpdump from inside the sbc container (see capture.sh for why not a bystander container)
make check PCAP=./voice-capture.pcap             # fail if a pod-network IP leaked into SIP/SDP
make down     # full teardown
```

`test/sipp/media-flow-check.sh [kamailio-net1-ip]` plays a real recording through the stack (`uac-media.xml`) and confirms actual RTP reaches rtpengine's `net1` address - a successful SIP call alone doesn't prove that; `-rtp_echo` only echoes RTP it receives.

`sbc/scenarios/` has four SIPp scenarios: `uac.xml` and `uas.xml` are a matched pair (used by `make test`); `hold-reinvite.xml` and `long-call.xml` are UAC-side only and need a real in-dialog-aware peer, so they're run against Kamailio/FreeSWITCH starting Phase 2, not against `uas.xml` - see the shortcut note in `uas.xml`.

Versions used here (kind, kind's node image, Multus, Whereabouts, SIPp) are pinned in `dev-docs/versions.md`.
