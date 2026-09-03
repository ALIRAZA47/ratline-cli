#!/bin/sh
# Stop the service before the binary goes away, and clear the failed state it would
# otherwise leave behind.
#
# systemd remembers that a unit failed after its file is gone: without reset-failed
# the entry sits in `systemctl --failed` for ever, which is exactly what monitoring
# watches. The panel's database and configuration are left alone — removing the
# package must not delete the accounts, and it must not touch anything ratline owns.
set -eu

if command -v systemctl >/dev/null 2>&1; then
    systemctl stop ratline-panel.service    >/dev/null 2>&1 || true
    systemctl disable ratline-panel.service >/dev/null 2>&1 || true
    systemctl reset-failed ratline-panel.service >/dev/null 2>&1 || true
fi
