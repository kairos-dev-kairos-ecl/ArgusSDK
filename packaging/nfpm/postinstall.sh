#!/bin/sh
# Register the systemd unit. The service is intentionally NOT auto-started:
# the operator must populate /etc/argus-agent/agent.yaml first.
set -e

if command -v systemctl >/dev/null 2>&1; then
    systemctl daemon-reload || true
    echo "argus-agent installed."
    echo "  1. Edit /etc/argus-agent/agent.yaml"
    echo "  2. sudo systemctl enable --now argus-agent"
fi
