#!/usr/bin/env bash
# Address hygiene check: fails if any pod-network IP appears anywhere in a
# SIP or SDP message - headers (Via, Record-Route, Contact) included, not
# just the SDP body. An earlier version only scanned sip.msg_body (the SDP),
# which let pod-network IPs in SIP headers pass silently - see ADR
# 001-necessity-review.md's "defects found along the way". Pod CIDRs are
# read live from the cluster, never hardcoded, since kindnet's default can
# change.
set -euo pipefail

PCAP=${1:?usage: check-addresses.sh <pcap-file>}
NETSHOOT=docker.io/nicolaka/netshoot:v0.16

PODCIDRS=$(kubectl get nodes -o jsonpath='{range .items[*]}{.spec.podCIDR}{"\n"}{end}')
if [[ -z "$PODCIDRS" ]]; then
  echo "could not read pod CIDRs from cluster" >&2
  exit 1
fi

# -V decodes the full message (every SIP header plus the SDP body as text),
# not just one field - sip.msg_body alone misses header leaks.
TEXT=$(podman run --rm -v "$(dirname "$(realpath "$PCAP")"):/in" "$NETSHOOT" \
  tshark -r "/in/$(basename "$PCAP")" -Y 'sip || sdp' -V 2>/dev/null || true)

fail=0
while IFS= read -r cidr; do
  [[ -z "$cidr" ]] && continue
  prefix=$(echo "${cidr%/*}" | cut -d. -f1-2)
  if echo "$TEXT" | grep -q "$prefix\."; then
    echo "FAIL: pod-network prefix $prefix. (from podCIDR $cidr) found in SIP/SDP payload" >&2
    fail=1
  fi
done <<< "$PODCIDRS"

if [[ $fail -eq 0 ]]; then
  echo "OK: no pod-network IP found in SIP/SDP"
else
  exit 1
fi
