#!/bin/sh
# Nightly documentation run: publishes to the YouTrack Knowledge Base. Fails on the first error.
set -eu

CONFIG_DIR=/etc/youtrack-docgen
BIN=/opt/youtrack-docgen/youtrack-docgen

# The environment file defines NETBOX_TOKEN and YOUTRACK_TOKEN.
# Keep it readable only by the service user: chmod 600.
set -a
. "$CONFIG_DIR/env"
set +a

"$BIN" validate-config --config "$CONFIG_DIR/docgen.yaml"
"$BIN" publish --config "$CONFIG_DIR/docgen.yaml"
