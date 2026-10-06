#!/usr/bin/env bash
# Builds the Go server and the Swift app, assembles ScreenReader.app with the
# Go binary embedded, and codesigns both. Requires Go + Xcode toolchain.
# Optional (recommended, one-time): a self-signed codesigning identity named
# "ScreenReader Dev" — without it signing is ad-hoc and TCC grants reset on
# every rebuild (see README "ScreenReader app" section).
set -euo pipefail
cd "$(dirname "$0")"

# 1. Go server binary
(cd server && CGO_ENABLED=1 go build -o screenreader .)

# 2. Swift app binary (release)
(cd app && swift build -c release)

# 3. Assemble the .app bundle
APP="app/build/ScreenReader.app"
rm -rf "$APP"
mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Resources"
cp app/.build/release/ScreenReader "$APP/Contents/MacOS/ScreenReader"
cp app/Info.plist "$APP/Contents/Info.plist"
cp server/screenreader "$APP/Contents/Resources/screenreader"

# 4. Codesign (stable identity when available, else ad-hoc with a warning)
IDENTITY="ScreenReader Dev"
if security find-identity -v -p codesigning | grep -q "$IDENTITY"; then
  SIGN=(--force --sign "$IDENTITY")
else
  echo "warning: no '$IDENTITY' codesigning identity — using AD-HOC signing."
  echo "         TCC grants will NOT survive app rebuilds. Create the identity"
  echo "         once via Keychain Access → Certificate Assistant → Create a"
  echo "         New Certificate (name '$IDENTITY', kind Code Signing, self-signed)."
  SIGN=(--force --sign -)
fi
codesign "${SIGN[@]}" "$APP/Contents/Resources/screenreader"
codesign "${SIGN[@]}" "$APP"

echo "built: $APP"
echo "run:   open $APP"
