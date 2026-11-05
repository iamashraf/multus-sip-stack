SHELL := /usr/bin/env bash
export KIND_EXPERIMENTAL_PROVIDER = podman

CLUSTER := voice-lab

.PHONY: up down test capture check

# One command for the whole stack: the kind cluster (kind's own lifecycle,
# can't be a compose service), then the voice network + sbc container
# (compose.yaml, brought up/down as a single unit), then the kind edge
# workers get attached to that network, then the cluster addons.
up:
	kind get clusters | grep -qx "$(CLUSTER)" || kind create cluster --config kind.yaml
	podman-compose up -d --build
	./voice-net.sh up
	./addons.sh

down:
	./voice-net.sh down
	kind delete cluster --name $(CLUSTER) 2>/dev/null || true
	podman-compose down

# Harness self-test: proves the sbc image and scenario XML work, run
# loopback inside the sbc container. This does not exercise Kamailio or
# FreeSWITCH - those self-tests land with their own charts in Phases 2-4,
# pointed at the real net1/pool IPs instead of 127.0.0.1.
test:
	podman exec -d sbc sipp -sf /scenarios/uas.xml -i 127.0.0.1 -p 5061 -mi 127.0.0.1 -rtp_echo
	sleep 1
	podman exec sbc sipp -sf /scenarios/uac.xml -i 127.0.0.1 -p 5060 -mi 127.0.0.1 -rtp_echo -m 1 127.0.0.1:5061

capture:
	./capture.sh $(IFACE) $(OUT)

check:
	./check-addresses.sh $(PCAP)
