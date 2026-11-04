#!/usr/bin/env bash
# Proves media flows across the whole chain, not just one hop: a
# successful SIP call (uac.xml, make test) can still carry zero media
# (-rtp_echo only echoes what it receives), and checking only the SBC
# side would miss a break between rtpengine and FreeSWITCH. This plays a
# real recording (uac-media.xml) and captures both legs at once:
#   leg 1: SBC <-> rtpengine, on rtpengine's net1 address
#   leg 2: rtpengine <-> FreeSWITCH, on rtpengine's eth0 address
#
# Each capture is taken from an actual endpoint, never a bystander
# container on the same bridge/pod network - a bystander only sees
# broadcast/multicast traffic, never unicast between two OTHER hosts
# (normal switch/CNI behavior, not a bug; see the note in capture.sh).
#
# Also doubles as the project's address-hygiene check (no pod-network IP
# in SIP/SDP) on a capture that actually has a real call in it.
set -euo pipefail
cd "$(dirname "$0")/../../local"

KAM_IP=${1:-192.168.50.20}
SBC_REMOTE=/tmp/media-flow-check.pcap
SBC_OUT=$(pwd)/media-flow-check-sbc.pcap
RTPENGINE_REMOTE=/tmp/media-flow-check-internal.pcap
RTPENGINE_OUT=$(pwd)/media-flow-check-rtpengine.pcap

RTPENGINE_POD=$(kubectl -n voice get pod -l app=rtpengine -o jsonpath='{.items[0].metadata.name}')
RTPENGINE_ETH0_IP=$(kubectl -n voice get pod -l app=rtpengine -o jsonpath='{.items[0].status.podIP}')
RTPENGINE_NET1_IP=$(kubectl -n voice get pod -l app=rtpengine -o jsonpath='{.items[0].metadata.annotations.k8s\.v1\.cni\.cncf\.io/network-status}' \
  | python3 -c "import json,sys; print(next(i['ips'][0] for i in json.load(sys.stdin) if i['interface']=='net1'))")
FSW_POD_IP=$(kubectl -n voice get pod -l app=freeswitch -o jsonpath='{.items[0].status.podIP}')
echo "rtpengine: net1=$RTPENGINE_NET1_IP eth0=$RTPENGINE_ETH0_IP, freeswitch pod=$FSW_POD_IP"

kubectl -n voice exec "$RTPENGINE_POD" -- sh -c "command -v tcpdump >/dev/null || (apt-get update -qq && apt-get install -y -qq tcpdump)" >/dev/null 2>&1

# Leg 1: sbc's own view of its traffic to rtpengine.
podman exec sbc rm -f "$SBC_REMOTE"
podman exec sbc timeout 25 tcpdump -i eth0 -w "$SBC_REMOTE" &
SBC_CAP_PID=$!

# Leg 2: rtpengine's own view of its traffic to FreeSWITCH.
kubectl -n voice exec "$RTPENGINE_POD" -- timeout 25 tcpdump -i eth0 -w "$RTPENGINE_REMOTE" &
RTPENGINE_CAP_PID=$!

sleep 1
podman exec sbc sipp -sf /scenarios/uac-media.xml -i 192.168.50.5 -p 5060 -mi 192.168.50.5 -s 9196 -m 1 "$KAM_IP:5060"

wait "$SBC_CAP_PID" 2>/dev/null || true
wait "$RTPENGINE_CAP_PID" 2>/dev/null || true
rm -f "$SBC_OUT" "$RTPENGINE_OUT"
podman cp "sbc:$SBC_REMOTE" "$SBC_OUT"
kubectl -n voice cp "$RTPENGINE_POD:$RTPENGINE_REMOTE" "$RTPENGINE_OUT"

echo "--- address hygiene (no pod-network IP in SIP/SDP), both legs ---"
./check-addresses.sh "$SBC_OUT"
./check-addresses.sh "$RTPENGINE_OUT"

echo "--- leg 1: SBC -> rtpengine (net1) ---"
LEG1=$(podman run --rm -v "$(pwd):/in" docker.io/nicolaka/netshoot:v0.16 \
  tshark -r "/in/$(basename "$SBC_OUT")" -Y "udp && ip.src==192.168.50.5 && ip.dst==$RTPENGINE_NET1_IP && udp.length>20" 2>/dev/null | wc -l | tr -d ' ')
echo "RTP packets: $LEG1"

echo "--- leg 2: rtpengine (eth0) -> FreeSWITCH ---"
LEG2=$(podman run --rm -v "$(pwd):/in" docker.io/nicolaka/netshoot:v0.16 \
  tshark -r "/in/$(basename "$RTPENGINE_OUT")" -Y "udp && ip.src==$RTPENGINE_ETH0_IP && ip.dst==$FSW_POD_IP && udp.length>20" 2>/dev/null | wc -l | tr -d ' ')
echo "RTP packets: $LEG2"

if [ "$LEG1" -gt 0 ] && [ "$LEG2" -gt 0 ]; then
  echo "PASS: media flowed across the whole chain (SBC -> rtpengine -> FreeSWITCH), no pod-network IP ever reaching the SBC"
else
  echo "FAIL: leg 1 (SBC->rtpengine) packets=$LEG1, leg 2 (rtpengine->FreeSWITCH) packets=$LEG2 - at least one leg carried no media"
  exit 1
fi
