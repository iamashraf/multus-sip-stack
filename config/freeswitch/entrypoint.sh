#!/bin/sh
# net1 (voice VLAN, toward the SBC) and eth0 (pod network, toward Kamailio)
# IPs aren't known until the pod starts, so vars.xml is templated here
# rather than baked in - same pattern as config/kamailio/entrypoint.sh.
set -eu

NET1_IP=$(ip -4 -o addr show net1 2>/dev/null | awk '{print $4}' | cut -d/ -f1)
if [ -z "$NET1_IP" ]; then
  echo "FATAL: no net1 interface found - FreeSWITCH needs the voice-pool Multus attachment to anchor media with the SBC (see ADR 002)" >&2
  exit 1
fi
ETH0_IP=$(ip -4 -o addr show eth0 | awk '{print $4}' | cut -d/ -f1)

# Confirms the voice-pool NAD's routes actually send SBC-bound RTP out
# net1 - without them it leaves via eth0 and gets NATed (see ADR 002).
if [ -n "${SBC_MEDIA_IP:-}" ]; then
  if ! ip route get "$SBC_MEDIA_IP" | grep -q " dev net1 "; then
    echo "FATAL: route to SBC media IP $SBC_MEDIA_IP does not go via net1 - check the voice-pool NAD's routes" >&2
    exit 1
  fi
fi

sed \
  -e "s/__NET1_IP__/${NET1_IP}/g" \
  -e "s/__ETH0_IP__/${ETH0_IP}/g" \
  /etc/freeswitch/vars.xml.tmpl > /etc/freeswitch/vars.xml

exec /usr/bin/freeswitch
