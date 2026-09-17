#!/bin/bash
# Copies this case's fixture repository and the schema snapshot into the eval workspace.
set -eu
here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cp -R "$here/../fixtures/nextjs-shop/." .
cp "$here/../fixtures/ratline-schema.json" ./ratline-schema.json
