# Call recordings

Each dated directory holds real, decoded-audio proof of one full call lifecycle through the live stack (SBC → Kamailio → FreeSWITCH). `test/sipp/call-recording-check.sh` and `test/sipp/call-recording-check-connected.sh` generate these directories. Each run writes a fresh, timestamped directory, and none of the files inside are hand-edited.

## What's in each run's directory

| File | What it is |
|---|---|
| `call.pcap` | Full capture from the SBC's own interface, start to hangup |
| `audio.wav` | Decoded FreeSWITCH → SBC RTP payload (PCMU → WAV) |
| `packets-per-second.txt` | RTP packet counts per second, FreeSWITCH → SBC |
| `volume.txt` | `ffmpeg -af volumedetect` output for `audio.wav` |
| `address-hygiene.txt` | `check-addresses.sh` result (pod-IP leak check) |
| `summary.txt` | Pass or fail verdict |

## [20261104-204453](20261104-204453/): final validated run

The scenario `local/sbc/scenarios/uac-demo-hold.xml` calls FreeSWITCH's `demo_voice` extension (9199). It answers, listens to the real greeting, holds (re-INVITE `a=sendonly`), waits, unholds (re-INVITE `a=sendrecv`), listens to more real audio, and hangs up.

This run happened after fixing three real bugs that a load test surfaced on this same path (see ADR 002):

- a stale static dispatcher seed
- a `dispatcher-sync` bug that made it skip a whole poll tick whenever the live list was legitimately empty
- a missing `readinessProbe` on FreeSWITCH, which let a brand-new replica take a call before Sofia was listening

All three bugs are fixed. This recording shows that fixed, current state, not the first attempt.

- **Call completed end-to-end**: all 17 SIP messages (INVITE, hold, unhold, and BYE) got the expected response, with 0 failures.
- **Hold actually silenced the stream**, not just a 200'd re-INVITE: `packets-per-second.txt` shows FreeSWITCH → SBC RTP packets at seconds 10-12 (pre-hold), a gap at second 13 (on hold), and renewed packets at 14-16 (post-unhold).
- **Real audio, not silence**: `audio.wav` decodes to a mean volume of -31.9 dB and a max of -17.0 dB, consistent with the -31 dB reference figure in `dev-docs/verification.md`. The "more audio" segment is FreeSWITCH's hold-music file, `base256/music.wav`. It is genuine audio, but short (~0.5s per loop), not a long musical piece.
- **SIP headers stayed clean**: `check-addresses.sh` passed against the full decoded capture, headers included. This is live proof that [ADR 003](../../../dev-docs/adr/003-topos-state-store.md)'s `topos` and `topos_htable` closed the pod-IP leak that [ADR 001](../../../dev-docs/adr/001-necessity-review.md) found, without breaking the call.
- The dispatched FreeSWITCH replica varies run to run, because the dispatcher uses round-robin. This run landed on `192.168.50.130`. The script resolves which replica actually took the call from the capture itself, rather than assuming one.

## [20261105-214407](20261105-214407/): welcome → hold → connected (real two-way audio) → hangup

The scenario `local/sbc/scenarios/uac-call-flow.xml` calls FreeSWITCH's `demo_connected` extension (9200). It answers, listens to the real greeting, holds (re-INVITE `a=sendonly`), waits, unholds (re-INVITE `a=sendrecv`), sends real audio into `echo()`, and hangs up.

The `demo_voice` recording above, extension 9199, plays a fixed script regardless of hold state. This recording proves the connected phase is an actual two-way call: FreeSWITCH sends back the SBC's own audio, routed straight through `echo()`, not a pre-recorded prompt.

This scenario's audio (`pcap/g711a.pcap`) is PCMA (A-law, RTP payload type 8), not PCMU. The first version of this scenario negotiated PCMU, and FreeSWITCH correctly discarded every packet. That looked identical to a FreeSWITCH bug or a hold-handling bug, until a capture showed the payload type mismatch directly. The fix changed the scenario's SDP to `RTP/AVP 8` and `a=rtpmap:8 PCMA/8000`.

- **Call completed end-to-end**: all 17 SIP messages (INVITE, hold, unhold, and BYE) got the expected response, with 0 failures.
- **Hold actually silenced the stream**: `packets-per-second.txt` shows FreeSWITCH → SBC RTP at seconds 10-13 (welcome), then nothing at 14-16 (on hold).
- **The call actually reconnected, not just a 200'd re-INVITE**: RTP resumes at second 17, right after the unhold `200 OK`, and continues through second 21. FreeSWITCH echoes the SBC's own audio back in real time.
- **Real audio, not silence**: `audio.wav` decodes to a mean volume of -26.5 dB, max -6.2 dB.
- **SIP headers stayed clean**: `check-addresses.sh` passed, the same `topos` proof as the recording above.
- Before trusting this recording, the scenario ran two more times, each uncommitted, with consistent results: -26.5 dB both times, and the same gap-and-resume pattern.
