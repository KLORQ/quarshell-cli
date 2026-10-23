# SPDX-FileCopyrightText: Copyright (c) 2026 the quarshell authors
# SPDX-License-Identifier: Apache-2.0

ARG DEBIAN_BASE=mirror.gcr.io/library/debian:bookworm@sha256:2c037a04925515fdd6ea85ea14a682d0e79931f5e9f5d07b6dbfc6ba12f9e858
FROM ${DEBIAN_BASE}

ENV DEBIAN_FRONTEND=noninteractive \
    QUARSHELL_POLICY=/etc/quarshell/policy.yaml \
    QUARSHELL_LOG_DIR=/tmp/quarshell-logs

RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates \
    && rm -rf /var/lib/apt/lists/* \
    && groupadd --system --gid 10001 quarshell \
    && useradd --uid 10001 --gid quarshell --no-create-home --home-dir /nonexistent --shell /usr/sbin/nologin quarshell \
    && mkdir -p /etc/quarshell /tmp/quarshell-logs \
    && chown -R quarshell:quarshell /tmp/quarshell-logs

COPY --chmod=0555 quarshell /usr/local/bin/quarshell
COPY --chmod=0444 default.yaml /etc/quarshell/policy.yaml

USER 10001:10001
EXPOSE 3128
ENTRYPOINT ["/usr/local/bin/quarshell"]
CMD ["proxy", "--listen", "0.0.0.0:3128", "--policy", "/etc/quarshell/policy.yaml"]
