#!/bin/sh
# Create the dedicated unprivileged service account before files are installed.
set -e

if ! getent group argus-agent >/dev/null 2>&1; then
    groupadd --system argus-agent
fi

if ! getent passwd argus-agent >/dev/null 2>&1; then
    useradd --system --gid argus-agent --no-create-home \
        --home-dir /var/lib/argus-agent --shell /usr/sbin/nologin argus-agent
fi
