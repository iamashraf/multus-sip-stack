#!/usr/bin/env bash
# Confirms media flows directly between the SBC and FreeSWITCH's net1
# address, with no relay in between (see ADR 002). A
# successful SIP call alone doesn't prove this (-rtp_echo only echoes what
# it receives). This plays a real recording (uac-media.xml) and captures
# the SBC's own view of the traffic - a single leg is enough now that the
# SBC and FreeSWITCH talk directly over the voice VLAN.
#
# The capture is taken from the SBC itself, never a bystander container on
# the same bridge/pod network - a bystander only sees broadcast/multicast
# traffic, never unicast between two OTHER hosts (normal switch/CNI
# behavior, not a bug; see the note in capture.sh).
#
# Also doubles as the project's address-hygiene check (no pod-network IP
# in SIP/SDP) on a capture that actually has a real call in it.
set -euo pipefail
cd "$(dirname "$0")/../../local"

KAM_IP=${1:-192.168.50.20}
SBC_REMOTE=/tmp/media-flow-check.pcap
SBC_OUT=$(pwd)/media-flow-check-sbc.pcap

FSW_POD=$(kubectl -n voice get pod -l app=freeswitch -o jsonpath='{.items[0].metadata.name}')
FSW_NET1_IP=$(kubectl -n voice get pod -l app=freeswitch -o jsonpath='{.items[0].metadata.annotations.k8s\.v1\.cni\.cncf\.io/network-status}' \
  | python3 -c "import json,sys; print(next(i['ips'][0] for i in json.load(sys.stdin) if i['interface']=='net1'))")
echo "freeswitch: net1=$FSW_NET1_IP (pod $FSW_POD)"

podman exec sbc rm -f "$SBC_REMOTE"
podman exec sbc timeout 25 tcpdump -i eth0 -w "$SBC_REMOTE" &
SBC_CAP_PID=$!

sleep 1
podman exec sbc sipp -sf /scenarios/uac-media.xml -i 192.168.50.5 -p 5060 -mi 192.168.50.5 -s 9196 -m 1 "$KAM_IP:5060"

wait "$SBC_CAP_PID" 2>/dev/null || true
rm -f "$SBC_OUT"
podman cp "sbc:$SBC_REMOTE" "$SBC_OUT"

echo "--- address hygiene (no pod/service-network IP in SIP/SDP) ---"
./check-addresses.sh "$SBC_OUT"

echo "--- direct path: SBC <-> FreeSWITCH net1 ---"
OUT_PACKETS=$(podman run --rm -v "$(pwd):/in" docker.io/nicolaka/netshoot:v0.16 \
  tshark -r "/in/$(basename "$SBC_OUT")" -Y "udp && ip.src==192.168.50.5 && ip.dst==$FSW_NET1_IP && udp.length>20" 2>/dev/null | wc -l | tr -d ' ')
echo "RTP packets SBC -> FreeSWITCH: $OUT_PACKETS"

IN_PACKETS=$(podman run --rm -v "$(pwd):/in" docker.io/nicolaka/netshoot:v0.16 \
  tshark -r "/in/$(basename "$SBC_OUT")" -Y "udp && ip.src==$FSW_NET1_IP && ip.dst==192.168.50.5 && udp.length>20" 2>/dev/null | wc -l | tr -d ' ')
echo "RTP packets FreeSWITCH -> SBC: $IN_PACKETS"

if [ "$OUT_PACKETS" -gt 0 ] && [ "$IN_PACKETS" -gt 0 ]; then
  echo "PASS: media flowed directly between the SBC and FreeSWITCH's net1, no relay in between"
else
  echo "FAIL: SBC->FreeSWITCH packets=$OUT_PACKETS, FreeSWITCH->SBC packets=$IN_PACKETS - at least one direction carried no media"
  exit 1
fi
