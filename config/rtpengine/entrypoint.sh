#!/bin/sh
# Bridges net1 (public, toward the SBC) and eth0 (internal, toward
# Kamailio/FreeSWITCH). IPs aren't known until the pod starts (static on
# net1, pod IP on eth0), so they're read here rather than baked in.
set -eu

NET1_IP=$(ip -4 -o addr show net1 | awk '{print $4}' | cut -d/ -f1)
ETH0_IP=$(ip -4 -o addr show eth0 | awk '{print $4}' | cut -d/ -f1)

exec rtpengine --foreground --log-stderr --log-level=6 --log-level-control=7 \
  --interface "public/${NET1_IP}" \
  --interface "internal/${ETH0_IP}" \
  --listen-ng "${ETH0_IP}:2223" \
  --port-min "${RTP_PORT_MIN:-30000}" \
  --port-max "${RTP_PORT_MAX:-30100}"
