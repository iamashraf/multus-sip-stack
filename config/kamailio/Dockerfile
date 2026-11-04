# Debian's own kamailio package - trusted upstream, no custom build.
# dispatcher.so and rtpengine.so both ship in the base package.
FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends kamailio iproute2 \
  && rm -rf /var/lib/apt/lists/*
COPY entrypoint.sh /entrypoint.sh
COPY kamailio.cfg.tmpl /etc/kamailio/kamailio.cfg.tmpl
RUN chmod +x /entrypoint.sh
ENTRYPOINT ["/entrypoint.sh"]
