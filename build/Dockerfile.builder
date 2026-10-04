FROM debian:trixie-slim
RUN apt-get update && apt-get install -y --no-install-recommends \
      build-essential bc bison flex libelf-dev libssl-dev xz-utils cpio \
      ca-certificates curl kmod rsync python3 gzip zstd file bsdextrautils \
    && rm -rf /var/lib/apt/lists/*
WORKDIR /src
