# Local lab

A kind cluster on one laptop that reproduces the production network shape. It lets you build and test Phases 1-5 before anything touches real RKE2. The same Helm charts deploy here and on RKE2. Only `environments/local/values.yaml` differs.

## Layout

| Node role | What it is |
|---|---|
| control-plane | kind control plane |
| voice x3 (`voice-role=voice`, tainted `voice-node=true:NoSchedule`) | Attached to the `voice` network via a second interface, same as production voice nodes. Kamailio and FreeSWITCH both schedule here now. Kamailio exchanges SIP with the SBC. FreeSWITCH exchanges media with the SBC directly (see ADR 002). |

`compose.yaml` declares the `voice` network and the `sbc` container (a SIPp-based stand-in for the real SBC, on a fixed IP) as one unit. `podman-compose up` and `podman-compose down` bring up or tear down both together. kind owns its own node containers, so they can't be compose services. `voice-net.sh` attaches the three kind-managed voice workers to that same network afterward.

## Known differences from production

- **kind instead of RKE2, kindnet instead of Canal** as the primary CNI. Multus runs alongside it the same way it will on RKE2.
- **A podman bridge network instead of a real VLAN**, so there's no switch MAC-per-port limit to work around. `environments/local/values.yaml` sets `voiceNetwork.attachmentType: macvlan` as the local default. Set it to `ipvlan` to test that path too.
- **No real SBC behavior.** The fake SBC (SIPp) sends and receives calls. It does no topology hiding or header manipulation, unlike a production SBC.

## Docker vs. Podman

This machine has Podman, not Docker. Bring `compose.yaml` up with `podman-compose`, not `podman compose`. `podman compose` shells out to the real `docker-compose` binary, which hits an image-commit/policy incompatibility on this machine. `kind` uses Podman through `KIND_EXPERIMENTAL_PROVIDER=podman`, set in the `Makefile`. `voice-net.sh` uses plain `podman network connect`. Nothing about the network model changes, only the CLI.

Two Podman-on-macOS gotchas came up while building this, in case they resurface:

- **Bind mounts work only under `$HOME`.** Podman's machine VM shares `$HOME` through virtiofs, but not `/tmp`. `capture.sh` and `check-addresses.sh` default their output to the current directory for this reason. Don't redirect them to `/tmp`. Pass `-v` an absolute path, never a relative one. A relative path like `.` silently becomes an empty named volume instead of a bind mount.
- **`policy.json` must allow the `containers-storage` transport.** If `podman build` or `podman-compose build` fails with `Source image rejected ... containers-storage:[...] is rejected by policy`, the machine's `~/.config/containers/policy.json` has no exception for locally-built images. Add one: `"containers-storage": {"": [{"type": "insecureAcceptAnything"}]}` under `transports`. The exception affects only images already in local storage, not remote pulls.

## Usage

```
cd local
make up       # kind cluster, voice network + sbc container (compose.yaml), Multus + Whereabouts
make test     # harness self-test: SIPp loopback inside the sbc container (see Makefile comment: this doesn't touch Kamailio or FreeSWITCH yet)
make capture IFACE=eth0                          # tcpdump from inside the sbc container (see capture.sh for why not a bystander container)
make check PCAP=./voice-capture.pcap             # fail if a pod-network IP leaked into SIP or SDP
make down     # full teardown
```

`test/sipp/media-flow-check.sh [kamailio-net1-ip]` plays a real recording through the stack (`uac-media.xml`) and confirms actual RTP reaches FreeSWITCH's `net1` address directly. A successful SIP call alone doesn't prove that. `-rtp_echo` echoes only RTP it receives.

`sbc/scenarios/` holds the baseline matched pair used by `make test` (`uac.xml`, `uas.xml`), plus scenarios added for later verification work: media flow (`uac-media.xml`), demo recordings (`uac-demo.xml`, `uac-demo-hold.xml`), and the full welcome → hold → connected → hangup call-flow proof (`uac-call-flow.xml`). `hold-reinvite.xml` and `long-call.xml` are UAC-side only. They need a real in-dialog-aware peer, so they run against Kamailio and FreeSWITCH starting Phase 2, not against `uas.xml` (see the shortcut note in `uas.xml`).

`dev-docs/versions.md` pins the versions used here: kind, kind's node image, Multus, Whereabouts, and SIPp.
