#!/usr/bin/env bash
# Unsigned beta builds (TRD §11.7): Windows x64/arm64 .exe (self-installing,
# per-user) and macOS arm64/x64 .app in .dmg (ad-hoc signed, not notarized).
# Usage: bash client/build-beta.sh   → dist/beta/
set -euo pipefail
ROOT=$(cd "$(dirname "$0")/.." && pwd)
APP="$ROOT/client/app"; PKG="$ROOT/client/packaging"; OUT=${OUT_DIR:-"$ROOT/dist/beta"}
VERSION=${VERSION:-0.1.0-beta.1}
ORIGIN=${ORIGIN:-https://jieshangai.com}
export GOTOOLCHAIN=auto CGO_ENABLED=0
LD="-s -w -X main.version=$VERSION -X main.defaultOrigin=$ORIGIN"
WORK=$(mktemp -d); trap 'rm -rf "$WORK"' EXIT
rm -rf "$OUT"; mkdir -p "$OUT"

# icons
rsvg-convert -w 1024 -h 1024 "$PKG/icon.svg" -o "$WORK/icon1024.png"
for s in 16 32 48 128 256 512; do rsvg-convert -w $s -h $s "$PKG/icon.svg" -o "$WORK/icon$s.png"; done
(cd "$APP" && GOOS=windows go generate ./... >/dev/null)   # include Windows-only ps1 directives

# ---- Windows ----
cat >"$WORK/winres.json" <<J
{"RT_GROUP_ICON":{"APP":{"0000":["icon256.png","icon48.png","icon32.png","icon16.png"]}},
 "RT_MANIFEST":{"#1":{"0409":{"identity":{"name":"com.jieshangai.client","version":"0.1.0.0"},"description":"接上AI","minimum-os":"win10","execution-level":"as invoker","dpi-awareness":"per monitor v2","use-common-controls-v6":true}}},
 "RT_VERSION":{"#1":{"0000":{"fixed":{"file_version":"0.1.0.0","product_version":"0.1.0.0"},"info":{"0409":{"ProductName":"接上AI","FileDescription":"接上AI","CompanyName":"接上AI","ProductVersion":"$VERSION","FileVersion":"$VERSION","OriginalFilename":"接上AI.exe"}}}}}}
J
(cd "$APP" && go run github.com/tc-hib/go-winres@v0.3.3 make --in "$WORK/winres.json" --arch amd64,arm64 --out rsrc >/dev/null)
for arch in amd64 arm64; do
  name=$([ $arch = amd64 ] && echo x64 || echo arm64)
  (cd "$APP" && GOOS=windows GOARCH=$arch go build -trimpath -ldflags "$LD -H=windowsgui" -o "$OUT/jieshangai-win-$name.exe" .)
done
rm -f "$APP"/rsrc_windows_*.syso

# ---- macOS ----
if [ "${WINDOWS_ONLY:-0}" != "1" ]; then
png2icns "$WORK/icon.icns" "$WORK/icon16.png" "$WORK/icon32.png" "$WORK/icon128.png" "$WORK/icon256.png" "$WORK/icon512.png" >/dev/null
for arch in arm64 amd64; do
  name=$([ $arch = amd64 ] && echo x64 || echo arm64)
  stage="$WORK/dmg-$name"; app="$stage/接上AI.app"
  mkdir -p "$app/Contents/MacOS" "$app/Contents/Resources"
  (cd "$APP" && GOOS=darwin GOARCH=$arch go build -trimpath -ldflags "$LD" -o "$app/Contents/MacOS/jieshangai" .)
  sed "s/__VERSION__/$VERSION/g" "$PKG/Info.plist.tmpl" >"$app/Contents/Info.plist"
  printf 'APPL????' >"$app/Contents/PkgInfo"
  cp "$WORK/icon.icns" "$app/Contents/Resources/icon.icns"
  if command -v rcodesign >/dev/null || [ -x "$HOME/.cargo/bin/rcodesign" ]; then
    "${RCODESIGN:-$HOME/.cargo/bin/rcodesign}" sign "$app" >/dev/null 2>&1   # ad-hoc seal, no Apple ID
  fi
  ln -s /Applications "$stage/Applications"
  (cd "$stage" && zip -qry --symlinks "$OUT/jieshangai-mac-$name.app.zip" "接上AI.app")
  genisoimage -quiet -V "接上AI" -D -R -apple -no-pad -o "$OUT/jieshangai-mac-$name.dmg" "$stage"
done
fi
(cd "$OUT" && sha256sum * >SHA256SUMS.txt)
ls -la "$OUT"
