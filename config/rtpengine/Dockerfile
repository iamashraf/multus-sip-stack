# Debian's own rtpengine package - trusted upstream, no custom build.
FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends rtpengine iproute2 \
  && rm -rf /var/lib/apt/lists/*
COPY entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh
ENTRYPOINT ["/entrypoint.sh"]
