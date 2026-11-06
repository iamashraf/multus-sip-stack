# Verification

How the stack is actually proven to work, not just assumed to. A successful SIP call can still carry zero media. `-rtp_echo` only echoes RTP it receives, and a 200 OK says nothing about whether audio ever moved. Every check here is run against a real call, with evidence (a capture, a decoded WAV, a hygiene check), not just a passing exit code.

[Interactive sequence diagram](../docs/e2e-call-verification.html): the full SIP + RTP flow for one real call, including where the reverse-RTP bug lived and how it was proven fixed.

## What "verified" means here

- **Signaling succeeding is not enough.** `test/sipp/media-flow-check.sh` plays a real recording through the stack and confirms RTP actually reaches FreeSWITCH's voice-VLAN address directly (no relay in between, see ADR 002).
- **Media has to be captured from a real endpoint.** `local/capture.sh` runs `tcpdump` inside the SBC container itself. A bystander container on the same bridge only sees broadcast/multicast traffic. Unicast between two other hosts (e.g. the SBC and a Kamailio pod) isn't flooded to a third port, by normal switch/CNI behavior, not a bug.
- **No pod-network IP may leak into SIP or SDP.** `local/check-addresses.sh <pcap>` reads pod CIDRs live from the cluster and fails if any of them show up in a capture's SIP/SDP payload.
- **Audio has to decode to something audible, not just "packets arrived."** Extract the RTP payload with `tshark`, decode it with `ffmpeg`, then run `ffmpeg -af volumedetect` on the result. A real signal shows a mean volume well above silence (around -31 dB in the calls run here), not near -inf.

## The call used for this proof

`local/sbc/scenarios/uac-demo.xml` drives FreeSWITCH's `demo_voice` extension (9199). It plays a greeting, hold music, and a closing phrase, then hangs up itself, so the test waits for FreeSWITCH's own BYE instead of sending one. That's what makes it a real end-to-end proof rather than a one-way echo test.

```
podman exec sbc sipp -sf /scenarios/uac-demo.xml \
  -i 192.168.50.5 -p 5060 -mi 192.168.50.5 -s 9199 -m 1 <kamailio-net1-ip>:5060
```

Capture at three points at once during that call:

```
podman exec sbc tcpdump -i eth0 -w sbc.pcap &
kubectl -n voice exec <rtpengine-pod> -- tcpdump -i eth0 -w eth0.pcap &
kubectl -n voice exec <rtpengine-pod> -- tcpdump -i net1 -w net1.pcap &
```

Then extract and decode the reverse leg (FreeSWITCH → rtpengine → SBC) from the SBC's own capture:

```
tshark -r sbc.pcap -Y "ip.src==<rtpengine-net1-ip> && ip.dst==<sbc-ip>" \
  -T fields -e rtp.payload | tr -d '\n' | xxd -r -p > reverse.ul
ffmpeg -f mulaw -ar 8000 -ac 1 -i reverse.ul reverse.wav
ffmpeg -i reverse.wav -af volumedetect -f null -
```

## Known gaps

- **"The call used for this proof" above predates ADR 002.** rtpengine has since been removed; those capture commands and the three-point capture (`eth0.pcap`/`net1.pcap` on an rtpengine pod that no longer exists) describe the old path and are kept as a historical record of how the original reverse-RTP bug was debugged, not a reproducible script. The committed, repeatable equivalent for the current (direct SBC-to-FreeSWITCH) path is `test/sipp/media-flow-check.sh`.
- FreeSWITCH runs as a small, not-yet-HA-proven pool. None of this re-verifies HA under the new direct-media path yet.
- `test/sipp/media-flow-check.sh` currently covers the echo extension (9196), not `demo_voice`. `test/sipp/call-recording-check.sh` does cover `demo_voice`, including a hold/unhold cycle, with a committed decoded-audio recording; see `test/sipp/recordings/`.
- The SIP-header pod-IP leak this doc's address-hygiene check used to catch is now closed (Kamailio's `topos`/`topos_htable`, [ADR 003](adr/003-topos-state-store.md)). `check-addresses.sh` passes clean on a current capture.
