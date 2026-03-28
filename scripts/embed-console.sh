#!/usr/bin/env bash
# Build claw-console and copy static assets into console/dist/ for Go embedding.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
ROOT_DIR="$(dirname "$SCRIPT_DIR")"
CONSOLE_DIR="${CLAW_CONSOLE_DIR:-$ROOT_DIR/../claw-console}"
DEST_DIR="$ROOT_DIR/console/dist"

if [ ! -d "$CONSOLE_DIR" ]; then
  echo "claw-console not found at $CONSOLE_DIR"
  echo "Clone it first:  git clone git@github.com:claw-agent-operators/claw-console.git $CONSOLE_DIR"
  exit 1
fi

echo "Building claw-console from $CONSOLE_DIR..."
(cd "$CONSOLE_DIR" && npm ci --silent && npm run build)

echo "Copying dist/ → $DEST_DIR"
rm -rf "$DEST_DIR"
cp -r "$CONSOLE_DIR/dist" "$DEST_DIR"

echo "Done. Run 'make build' to embed into claw binary."
