#!/usr/bin/env bash
# Captures from inside the sbc container, not from a bystander container
# attached to the same podman network. A bystander only sees broadcast/
# multicast on a bridge - unicast traffic between two OTHER hosts (e.g.
# sbc <-> a Kamailio pod) isn't flooded to a third port; that's normal
# switch MAC-learning, not a bug. Capturing on an actual endpoint's own
# interface sees everything to/from it.
set -euo pipefail

IFACE=${1:-eth0}
OUT=${2:-$(pwd)/voice-capture.pcap}
REMOTE=/tmp/capture.pcap

mkdir -p "$(dirname "$OUT")"
podman exec sbc rm -f "$REMOTE"
podman exec -d sbc sh -c "tcpdump -i $IFACE -w $REMOTE & echo \$! > /tmp/capture.pid"
echo "Capturing on sbc's $IFACE -> $OUT. Ctrl-C to stop."

cleanup() {
  podman exec sbc sh -c 'kill -INT "$(cat /tmp/capture.pid)"' 2>/dev/null || true
  sleep 1
  podman cp "sbc:$REMOTE" "$OUT" 2>/dev/null || true
  echo "Saved capture to $OUT"
}
trap cleanup INT TERM EXIT

sleep infinity
