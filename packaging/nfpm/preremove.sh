#!/bin/sh
# Stop and disable the service before files are removed.
set -e

if command -v systemctl >/dev/null 2>&1; then
    systemctl disable --now argus-agent || true
fi
