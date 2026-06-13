#!/bin/sh
# Remove the argus-agent launchd daemon and binary. Leaves config and state.
set -e

PLIST=/Library/LaunchDaemons/org.kairos-foundation.argus-agent.plist

if [ "$(id -u)" -ne 0 ]; then
    echo "uninstall.sh must be run as root (use sudo)" >&2
    exit 1
fi

if [ -f "$PLIST" ]; then
    launchctl unload -w "$PLIST" 2>/dev/null || true
    rm -f "$PLIST"
fi

rm -f /usr/local/bin/argus-agent

echo "Uninstalled. Config in /etc/argus-agent and state in /var/lib/argus-agent were left in place."
