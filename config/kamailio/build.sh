#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")"
podman build -t localhost/kamailio:local .
for node in voice-lab-worker voice-lab-worker2 voice-lab-worker3 voice-lab-control-plane; do
  podman save localhost/kamailio:local | podman exec -i "$node" ctr -n k8s.io images import -
done
