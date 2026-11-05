#!/usr/bin/env bash
# Full-lifecycle proof with a real, decoded audio recording: answer, listen
# to FreeSWITCH's real greeting (demo_voice, 9199), hold (re-INVITE
# a=sendonly), wait, unhold (re-INVITE a=sendrecv), listen to more real
# audio, hang up. Captured at the SBC's own interface (single leg - no
# relay in the path, see ADR 002). Proves two things a passing SIP
# transaction log can't: real audio actually moved before/after hold, and
# hold actually silenced the stream (not just that the re-INVITE got a
# 200 OK) - both now going through topos-enabled Kamailio (ADR 003), so
# this also re-proves the call still completes correctly with topos
# active.
#
# Usage: ./call-recording-check.sh [kamailio-net1-ip] [output-dir]
set -euo pipefail
cd "$(dirname "$0")/../../local"

KAM_IP=${1:-192.168.50.20}
OUT_DIR=${2:-$(pwd)/../test/sipp/recordings/$(date +%Y%m%d-%H%M%S)}
mkdir -p "$OUT_DIR"

SBC_REMOTE=/tmp/call-recording-check.pcap
SBC_OUT="$OUT_DIR/call.pcap"

FSW_POD=$(kubectl -n voice get pod -l app=freeswitch -o jsonpath='{.items[0].metadata.name}')
FSW_NET1_IP=$(kubectl -n voice get pod -l app=freeswitch -o jsonpath='{.items[0].metadata.annotations.k8s\.v1\.cni\.cncf\.io/network-status}' \
  | python3 -c "import json,sys; print(next(i['ips'][0] for i in json.load(sys.stdin) if i['interface']=='net1'))")
echo "freeswitch: net1=$FSW_NET1_IP (pod $FSW_POD, may not be the one dispatched to - resolved from the pcap below if not)"

podman exec sbc rm -f "$SBC_REMOTE"
podman exec sbc timeout 25 tcpdump -i eth0 -w "$SBC_REMOTE" &
SBC_CAP_PID=$!

sleep 1
podman exec sbc sipp -sf /scenarios/uac-demo-hold.xml -i 192.168.50.5 -p 5060 -mi 192.168.50.5 -s 9199 -m 1 "$KAM_IP:5060"

wait "$SBC_CAP_PID" 2>/dev/null || true
podman cp "sbc:$SBC_REMOTE" "$SBC_OUT"

echo "--- address hygiene (topos should keep this clean too) ---"
./check-addresses.sh "$SBC_OUT" | tee "$OUT_DIR/address-hygiene.txt"

# The dispatcher may have sent this call to either FreeSWITCH replica -
# find out which one actually exchanged RTP with the SBC, same approach
# as media-flow-check.sh.
NETSHOOT="docker.io/nicolaka/netshoot:v0.16"
ACTUAL_FSW_IP=$(podman run --rm -v "$OUT_DIR:/in" "$NETSHOOT" \
  tshark -r "/in/call.pcap" -Y "udp && ip.dst==192.168.50.5 && udp.length>20" -T fields -e ip.src 2>/dev/null | sort -u | head -1)
echo "RTP from FreeSWITCH observed from: $ACTUAL_FSW_IP"

echo "--- per-second RTP packet counts, FreeSWITCH -> SBC (hold should show a gap) ---"
podman run --rm -v "$OUT_DIR:/in" "$NETSHOOT" \
  tshark -r "/in/call.pcap" -Y "udp && ip.src==$ACTUAL_FSW_IP && ip.dst==192.168.50.5 && udp.length>20" \
  -T fields -e frame.time_relative 2>/dev/null | awk '{printf "%d\n", $1}' | sort -n | uniq -c \
  | tee "$OUT_DIR/packets-per-second.txt"

echo "--- extracting + decoding FreeSWITCH -> SBC audio ---"
podman run --rm -v "$OUT_DIR:/in" "$NETSHOOT" \
  tshark -r "/in/call.pcap" -Y "ip.src==$ACTUAL_FSW_IP && ip.dst==192.168.50.5 && rtp.p_type==0" \
  -T fields -e rtp.payload 2>/dev/null | tr -d '\n' | xxd -r -p > "$OUT_DIR/audio.ul"

ffmpeg -y -f mulaw -ar 8000 -ac 1 -i "$OUT_DIR/audio.ul" "$OUT_DIR/audio.wav" -loglevel error
ffmpeg -i "$OUT_DIR/audio.wav" -af volumedetect -f null - 2>&1 | grep -E "mean_volume|max_volume" | tee "$OUT_DIR/volume.txt"
rm -f "$OUT_DIR/audio.ul"

MEAN_DB=$(grep -oE 'mean_volume: -?[0-9.]+' "$OUT_DIR/volume.txt" | grep -oE '\-?[0-9.]+$')
HOLD_GAP=$(awk '{if (prev != "" && $2 - prev >= 2) print "yes"; prev=$2}' "$OUT_DIR/packets-per-second.txt" | head -1)

echo "--- summary ---" | tee "$OUT_DIR/summary.txt"
if [ -n "$MEAN_DB" ] && awk "BEGIN{exit !($MEAN_DB > -45)}"; then
  echo "PASS: real audio decoded, mean volume ${MEAN_DB}dB (not silence)" | tee -a "$OUT_DIR/summary.txt"
else
  echo "FAIL: decoded audio is near silence (mean volume ${MEAN_DB:-unknown}dB)" | tee -a "$OUT_DIR/summary.txt"
  exit 1
fi
if [ "$HOLD_GAP" = "yes" ]; then
  echo "PASS: a >=2s gap in FreeSWITCH->SBC RTP was observed (hold silenced the stream)" | tee -a "$OUT_DIR/summary.txt"
else
  echo "FAIL: no gap found in FreeSWITCH->SBC RTP - hold may not have silenced the stream" | tee -a "$OUT_DIR/summary.txt"
  exit 1
fi
echo "Artifacts: $OUT_DIR"
