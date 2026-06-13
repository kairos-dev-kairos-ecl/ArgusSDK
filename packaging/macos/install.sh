#!/bin/sh
# Install argus-agent as a macOS launchd daemon.
# Run from the extracted release archive: sudo ./install.sh
set -e

PREFIX=/usr/local/bin
CONFDIR=/etc/argus-agent
PLIST=/Library/LaunchDaemons/org.kairos-foundation.argus-agent.plist

if [ "$(id -u)" -ne 0 ]; then
    echo "install.sh must be run as root (use sudo)" >&2
    exit 1
fi

# Paths are relative to the extracted release archive root.
install -m 0755 argus-agent "$PREFIX/argus-agent"

mkdir -p "$CONFDIR"
if [ ! -f "$CONFDIR/agent.yaml" ]; then
    install -m 0644 config/agent.example.yaml "$CONFDIR/agent.yaml"
    echo "Installed default config to $CONFDIR/agent.yaml — edit it before starting."
fi

mkdir -p /var/lib/argus-agent /var/log/argus-agent

install -m 0644 packaging/launchd/org.kairos-foundation.argus-agent.plist "$PLIST"

echo "Installed. Edit $CONFDIR/agent.yaml, then start with:"
echo "  sudo launchctl load -w $PLIST"
