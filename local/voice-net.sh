#!/usr/bin/env bash
# Attaches the kind-managed voice worker containers to the "voice" network.
# The network itself (and the sbc container) is owned by compose.yaml -
# `podman compose up` must run first. kind manages its own node containers,
# so they can't be declared in compose.yaml; this script is what bridges
# the two. All three workers need this now: FreeSWITCH anchors media
# directly with the SBC over net1 (see ADR 002), not just Kamailio.
set -euo pipefail

NETWORK=voice
VOICE_NODES=(voice-lab-worker voice-lab-worker2 voice-lab-worker3)

connect_edges() {
  for node in "${VOICE_NODES[@]}"; do
    connect "$node"
  done
}

connect() {
  local container=$1 ip=${2:-}
  if podman inspect "$container" --format '{{json .NetworkSettings.Networks}}' | grep -q "\"$NETWORK\""; then
    return 0
  fi
  if [[ -n "$ip" ]]; then
    podman network connect --ip "$ip" "$NETWORK" "$container"
  else
    podman network connect "$NETWORK" "$container"
  fi
}

disconnect() {
  local container=$1
  podman network disconnect "$NETWORK" "$container" --force 2>/dev/null || true
}

disconnect_edges() {
  for node in "${VOICE_NODES[@]}"; do
    disconnect "$node"
  done
}

verify() {
  for node in "${VOICE_NODES[@]}"; do
    echo "--- $node ---"
    podman exec "$node" ip -4 addr show eth1
  done
}

case "${1:-}" in
  up) connect_edges ;;
  down) disconnect_edges ;;
  connect) connect "$2" "${3:-}" ;;
  disconnect) disconnect "$2" ;;
  verify) verify ;;
  *) echo "usage: $0 {up|down|connect <container> [ip]|disconnect <container>|verify}" >&2; exit 1 ;;
esac
