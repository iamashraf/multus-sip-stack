# Wraps the upstream image with our own entrypoint, which templates net1/
# eth0 into vars.xml and fails fast if net1 is missing (see ADR 002).
FROM ghcr.io/patrickbaus/freeswitch-docker:1.11.3
COPY entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh
ENTRYPOINT ["/entrypoint.sh"]
