#!/usr/bin/env bash
# Builds, signs (Developer ID + hardened runtime + timestamp) and notarizes
# "Screen Reader MCP.app", staples the ticket, and wraps it in a .dmg.
# Artifacts land in dist/ (the dev bundle in app/build/ is never touched).
#
# Local:  needs the Developer ID Application identity in the login keychain
#         and a stored profile: xcrun notarytool store-credentials ...
#         (default profile name "screenreader").
# CI:     set SIGN_IDENTITY + NOTARY_KEY_PATH/NOTARY_KEY_ID/NOTARY_ISSUER_ID
#         (see .github/workflows/release.yml).
#
# Usage:  [VERSION=v1.2.3] ./release.sh
set -euo pipefail
cd "$(dirname "$0")"

SIGN_IDENTITY="${SIGN_IDENTITY:-Developer ID Application}"
NOTARY_PROFILE="${NOTARY_PROFILE:-screenreader}"
VERSION="${VERSION:-$(/usr/libexec/PlistBuddy -c 'Print :CFBundleShortVersionString' app/Info.plist)}"
DIST="dist"
APP="$DIST/Screen Reader MCP.app"

# 1. Binaries (same steps as build.sh)
(cd server && CGO_ENABLED=1 go build -o screenreader .)
(cd app && swift build -c release)

# 2. Assemble the bundle in dist/
mkdir -p "$DIST"
rm -rf "$APP"
mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Resources"
cp app/.build/release/ScreenReader "$APP/Contents/MacOS/Screen Reader MCP"
cp app/Info.plist "$APP/Contents/Info.plist"
cp app/Resources/AppIcon.icns "$APP/Contents/Resources/AppIcon.icns"
cp server/screenreader "$APP/Contents/Resources/screenreader"

# 3. Sign inside-out. Hardened runtime + secure timestamp are notarization
#    requirements for every executable in the bundle.
codesign --force --options runtime --timestamp --sign "$SIGN_IDENTITY" \
  "$APP/Contents/Resources/screenreader"
codesign --force --options runtime --timestamp --sign "$SIGN_IDENTITY" "$APP"
codesign --verify --strict --verbose=2 "$APP"

# 4. Notarize. CI passes the ASC API key via env; local default is the
#    stored keychain profile.
NOTARY_ARGS=(--keychain-profile "$NOTARY_PROFILE")
if [[ -n "${NOTARY_KEY_PATH:-}" ]]; then
  NOTARY_ARGS=(--key "$NOTARY_KEY_PATH" --key-id "$NOTARY_KEY_ID" --issuer "$NOTARY_ISSUER_ID")
fi
ZIP="$DIST/screen-reader-mcp-$VERSION.zip"
rm -f "$ZIP"
ditto -ck --keepParent "$APP" "$ZIP"
if ! SUBMIT_OUT=$(xcrun notarytool submit "$ZIP" "${NOTARY_ARGS[@]}" --wait 2>&1); then
  echo "$SUBMIT_OUT"
  SUB_ID=$(printf '%s\n' "$SUBMIT_OUT" | grep -Eo '[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}' | head -1)
  [[ -n "$SUB_ID" ]] && xcrun notarytool log "$SUB_ID" "${NOTARY_ARGS[@]}" || true
  exit 1
fi
printf '%s\n' "$SUBMIT_OUT" | tail -3

# 5. Staple the ticket into the bundle, then wrap it in a .dmg and notarize
#    the .dmg itself — stapler only finds a ticket for a file Apple has a
#    notarization record for, so the container must be submitted separately.
xcrun stapler staple "$APP"
DMG="$DIST/screen-reader-mcp-$VERSION.dmg"
rm -f "$DMG"
hdiutil create -volname "Screen Reader MCP" -srcfolder "$APP" -ov -format UDZO "$DMG" >/dev/null 2>&1
xcrun notarytool submit "$DMG" "${NOTARY_ARGS[@]}" --wait
xcrun stapler staple "$DMG"
xcrun stapler validate "$DMG"
spctl --assess --type install --verbose "$APP" || echo "warning: spctl rejected the app (often over-strict on the build machine; stapler validate above is the real gate)"
( cd "$DIST" && shasum -a 256 "$(basename "$DMG")" > "$(basename "$DMG").sha256" )

echo "built: $DMG"
