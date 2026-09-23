#!/bin/bash
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"
APP="$ROOT/dist/State Server.app"
mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Resources"
swift build -c release
BIN_DIR="$(swift build -c release --show-bin-path)"
cp "$BIN_DIR/StateServerMac" "$APP/Contents/MacOS/StateServerMac"
go build -trimpath -ldflags "-s -w -X main.version=$(git rev-parse --short HEAD)" -o "$APP/Contents/Resources/state-server" ./cmd/state-server
go build -trimpath -ldflags '-s -w' -o "$APP/Contents/Resources/statectl" ./cmd/statectl
cp macos/Info.plist "$APP/Contents/Info.plist"
ICON_DIR="$ROOT/build/StateServer.iconset"
mkdir -p "$ICON_DIR"
ICON="$ROOT/macos/ServerIcon-1024.png"
for SIZE in 16 32 128 256 512; do
    sips -z "$SIZE" "$SIZE" "$ICON" --out "$ICON_DIR/icon_${SIZE}x${SIZE}.png" >/dev/null
    DOUBLE=$((SIZE * 2))
    sips -z "$DOUBLE" "$DOUBLE" "$ICON" --out "$ICON_DIR/icon_${SIZE}x${SIZE}@2x.png" >/dev/null
done
iconutil -c icns "$ICON_DIR" -o "$APP/Contents/Resources/StateServer.icns"
IDENTITY="${STATE_CODESIGN_IDENTITY:--}"
codesign --force --sign "$IDENTITY" "$APP/Contents/Resources/state-server"
codesign --force --sign "$IDENTITY" "$APP/Contents/Resources/statectl"
codesign --force --sign "$IDENTITY" "$APP"
codesign --verify --strict "$APP"
printf '%s\n' "$APP"
