#!/usr/bin/env bash
# Full-lifecycle proof with a real, decoded audio recording: answer, listen
# to FreeSWITCH's real greeting (demo_connected, 9200), hold (re-INVITE
# a=sendonly), wait, unhold (re-INVITE a=sendrecv), send real audio into
# echo() to prove the call is actually connected (two-way, not just a
# 200'd re-INVITE), hang up. Captured at the SBC's own interface (single
# leg - no relay in the path, see ADR 002). Unlike call-recording-check.sh
# (demo_voice/9199, which plays a fixed script regardless of hold state),
# this proves FreeSWITCH -> SBC audio actually RESUMES after unhold, not
# just that a re-INVITE got a 200 OK - the scenario's own audio (PCMA,
# see pcap/g711a.pcap) comes straight back.
#
# Usage: ./call-recording-check-connected.sh [kamailio-net1-ip] [output-dir]
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
podman exec sbc timeout 30 tcpdump -i eth0 -w "$SBC_REMOTE" &
SBC_CAP_PID=$!

sleep 1
podman exec sbc sipp -sf /scenarios/uac-call-flow.xml -i 192.168.50.5 -p 5060 -mi 192.168.50.5 -s 9200 -m 1 "$KAM_IP:5060"

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

echo "--- per-second RTP packet counts, FreeSWITCH -> SBC (hold should show a gap, then resume) ---"
podman run --rm -v "$OUT_DIR:/in" "$NETSHOOT" \
  tshark -r "/in/call.pcap" -Y "udp && ip.src==$ACTUAL_FSW_IP && ip.dst==192.168.50.5 && udp.length>20" \
  -T fields -e frame.time_relative 2>/dev/null | awk '{printf "%d\n", $1}' | sort -n | uniq -c \
  | tee "$OUT_DIR/packets-per-second.txt"

echo "--- extracting + decoding FreeSWITCH -> SBC audio (PCMA, this scenario's pcap is A-law) ---"
podman run --rm -v "$OUT_DIR:/in" "$NETSHOOT" \
  tshark -r "/in/call.pcap" -Y "ip.src==$ACTUAL_FSW_IP && ip.dst==192.168.50.5 && rtp.p_type==8" \
  -T fields -e rtp.payload 2>/dev/null | tr -d '\n' | xxd -r -p > "$OUT_DIR/audio.al"

ffmpeg -y -f alaw -ar 8000 -ac 1 -i "$OUT_DIR/audio.al" "$OUT_DIR/audio.wav" -loglevel error
ffmpeg -i "$OUT_DIR/audio.wav" -af volumedetect -f null - 2>&1 | grep -E "mean_volume|max_volume" | tee "$OUT_DIR/volume.txt"
rm -f "$OUT_DIR/audio.al"

MEAN_DB=$(grep -oE 'mean_volume: -?[0-9.]+' "$OUT_DIR/volume.txt" | grep -oE '\-?[0-9.]+$')
# First gap of >=2s (the hold), and the last second with any packets at all
# (must be well after that gap - proves FreeSWITCH -> SBC audio actually
# resumed post-unhold, not just that the re-INVITE got a 200 OK).
HOLD_GAP_SEC=$(awk '{if (prev != "" && $2 - prev >= 2) {print prev; exit} prev=$2}' "$OUT_DIR/packets-per-second.txt")
LAST_SEC=$(tail -1 "$OUT_DIR/packets-per-second.txt" | awk '{print $2}')

echo "--- summary ---" | tee "$OUT_DIR/summary.txt"
if [ -n "$MEAN_DB" ] && awk "BEGIN{exit !($MEAN_DB > -45)}"; then
  echo "PASS: real audio decoded, mean volume ${MEAN_DB}dB (not silence)" | tee -a "$OUT_DIR/summary.txt"
else
  echo "FAIL: decoded audio is near silence (mean volume ${MEAN_DB:-unknown}dB)" | tee -a "$OUT_DIR/summary.txt"
  exit 1
fi
if [ -n "$HOLD_GAP_SEC" ]; then
  echo "PASS: a >=2s gap in FreeSWITCH->SBC RTP was observed (hold silenced the stream)" | tee -a "$OUT_DIR/summary.txt"
else
  echo "FAIL: no gap found in FreeSWITCH->SBC RTP - hold may not have silenced the stream" | tee -a "$OUT_DIR/summary.txt"
  exit 1
fi
if [ -n "$LAST_SEC" ] && [ -n "$HOLD_GAP_SEC" ] && awk "BEGIN{exit !($LAST_SEC > $HOLD_GAP_SEC + 1)}"; then
  echo "PASS: FreeSWITCH->SBC RTP resumed after the hold gap (second $LAST_SEC, connected phase is real two-way audio)" | tee -a "$OUT_DIR/summary.txt"
else
  echo "FAIL: FreeSWITCH->SBC RTP never resumed after hold - unhold did not reconnect the call" | tee -a "$OUT_DIR/summary.txt"
  exit 1
fi
echo "Artifacts: $OUT_DIR"
