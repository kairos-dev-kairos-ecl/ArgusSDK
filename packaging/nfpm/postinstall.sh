#!/bin/sh
# Register and start the systemd service. The shipped default config runs in
# local mode and starts cleanly without credentials, so it is safe to enable
# now; edit /etc/argus-agent/agent.yaml (or push a managed config) and restart
# to point it at your destination.
set -e

if command -v systemctl >/dev/null 2>&1; then
    systemctl daemon-reload || true
    systemctl enable --now argus-agent || true
    echo "argus-agent installed and started (local mode)."
    echo "Configure /etc/argus-agent/agent.yaml, then: sudo systemctl restart argus-agent"
fi
