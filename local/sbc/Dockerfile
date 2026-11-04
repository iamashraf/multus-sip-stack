# Builds SIPp v3.8.0 from source, following SIPp's own docker/Dockerfile.debian
# build recipe (git submodule layout differs, so this clones the tag instead
# of using that file directly).
FROM debian:12-slim AS build
RUN apt-get update && apt-get install -y --no-install-recommends \
    binutils ca-certificates cmake g++ gcc git ninja-build \
    libgsl-dev libpcap-dev libncurses-dev libpugixml-dev libssl-dev \
  && rm -rf /var/lib/apt/lists/*
RUN git clone --depth 1 --branch v3.8.0 https://github.com/SIPp/sipp.git /sipp
WORKDIR /sipp
RUN cmake . -G Ninja -DCMAKE_BUILD_TYPE=Release \
      -DUSE_PCAP=1 -DUSE_GSL=1 -DUSE_SYSTEM_PUGIXML=ON \
  && ninja

FROM debian:12-slim
RUN apt-get update && apt-get install -y --no-install-recommends \
    libgsl27 libpcap0.8 libncursesw6 libssl3 libpugixml1v5 iproute2 iputils-ping tcpdump \
  && rm -rf /var/lib/apt/lists/*
COPY --from=build /sipp/sipp /usr/local/bin/sipp
COPY scenarios /scenarios
ENTRYPOINT ["sleep", "infinity"]
