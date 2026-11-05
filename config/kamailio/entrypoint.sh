#!/bin/sh
# net1 (SBC-facing) and eth0 (internal) IPs aren't known until the pod
# starts, so the config is templated here rather than baked in.
set -eu

NET1_IP=$(ip -4 -o addr show net1 | awk '{print $4}' | cut -d/ -f1)
ETH0_IP=$(ip -4 -o addr show eth0 | awk '{print $4}' | cut -d/ -f1)

sed \
  -e "s/__NET1_IP__/${NET1_IP}/g" \
  -e "s/__ETH0_IP__/${ETH0_IP}/g" \
  /etc/kamailio/kamailio.cfg.tmpl > /etc/kamailio/kamailio.cfg

exec kamailio -f /etc/kamailio/kamailio.cfg -DD -E
