# Debian's own kamailio package is too old for topos_htable (added in
# Kamailio 6.0, Dec 2024 - see ADR 003) - bookworm ships 5.6.x. Pull from
# Kamailio's own apt repo instead, pinned to the 6.0 series.
FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends gnupg wget ca-certificates \
  && wget -O- https://deb.kamailio.org/kamailiodebkey.gpg | gpg --dearmor -o /usr/share/keyrings/kamailio.gpg \
  && echo "deb [signed-by=/usr/share/keyrings/kamailio.gpg] http://deb.kamailio.org/kamailio60 bookworm main" > /etc/apt/sources.list.d/kamailio.list \
  && apt-get update && apt-get install -y --no-install-recommends kamailio iproute2 \
  && apt-get purge -y gnupg wget \
  && rm -rf /var/lib/apt/lists/*
COPY entrypoint.sh /entrypoint.sh
COPY kamailio.cfg.tmpl /etc/kamailio/kamailio.cfg.tmpl
RUN chmod +x /entrypoint.sh
ENTRYPOINT ["/entrypoint.sh"]
