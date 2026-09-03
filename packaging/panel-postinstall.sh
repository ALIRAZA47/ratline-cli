#!/bin/sh
# Deliberately does almost nothing.
#
# It does not start the panel and it does not enable the unit. A package that brings
# up a root-equivalent web service at install time gives the server to whoever
# reaches the port first — the first account created is the super admin, and there
# is no window in which that is safe on an unattended machine.
#
# `ratline-panel install` is the step that writes the configuration, creates the
# database and enables the service, and it is run by a person who is then in a
# position to claim it.
set -eu

printf '\n'
printf 'ratline-panel is installed but not running.\n\n'
printf 'Set it up:\n'
printf '    ratline-panel install\n\n'
printf 'It will listen on 127.0.0.1:8420. Reach it through a tunnel from your own\n'
printf 'machine and create the first super admin before putting it on a domain:\n\n'
printf '    ssh -L 8420:127.0.0.1:8420 <this-server>\n'
printf '    open http://localhost:8420\n\n'
printf 'Then, once DNS points here:\n'
printf '    ratline-panel domain set panel.example.com --email you@example.com\n\n'
