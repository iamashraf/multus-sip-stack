#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")"
podman build -t localhost/dispatcher-operator:local .
for node in voice-lab-worker voice-lab-worker2 voice-lab-worker3 voice-lab-control-plane; do
  podman save localhost/dispatcher-operator:local | podman exec -i "$node" ctr -n k8s.io images import -
done
