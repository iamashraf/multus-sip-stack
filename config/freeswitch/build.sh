#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")"
podman build -t localhost/freeswitch:local .
for node in voice-lab-worker voice-lab-worker2 voice-lab-worker3 voice-lab-control-plane; do
  # --all-platforms: the upstream base image is amd64-only; containerd's
  # default import filters it out on an arm64 (Apple Silicon) lab host
  # with "image might be filtered out" and no other error.
  podman save localhost/freeswitch:local | podman exec -i "$node" ctr -n k8s.io images import --all-platforms -
done
