#!/bin/sh
# Wraps a built macOS binary into "Kumiko Satellite.app" so Finder starts it without a terminal
# and the bundle can be signed, notarized and stapled.
set -eu
binary=${1:?binary}
version=${2:-dev}
out=${3:?output dir}
# CFBundleShortVersionString must be numeric; a branch build has no release version.
version=${version#v}
case "$version" in [0-9]*.[0-9]*.[0-9]*) ;; *) version=0.0.0 ;; esac
app="$out/Kumiko Satellite.app"
rm -rf "$app"
mkdir -p "$app/Contents/MacOS"
cp "$binary" "$app/Contents/MacOS/kumiko-satellite"
chmod +x "$app/Contents/MacOS/kumiko-satellite"
cat > "$app/Contents/Info.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>CFBundleIdentifier</key><string>de.kumiko.satellite</string>
  <key>CFBundleName</key><string>Kumiko Satellite</string>
  <key>CFBundleDisplayName</key><string>Kumiko Satellite</string>
  <key>CFBundleExecutable</key><string>kumiko-satellite</string>
  <key>CFBundlePackageType</key><string>APPL</string>
  <key>CFBundleInfoDictionaryVersion</key><string>6.0</string>
  <key>CFBundleShortVersionString</key><string>$version</string>
  <key>CFBundleVersion</key><string>$version</string>
  <key>LSMinimumSystemVersion</key><string>11.0</string>
  <key>NSHighResolutionCapable</key><true/>
  <key>NSAppTransportSecurity</key><dict><key>NSAllowsLocalNetworking</key><true/></dict>
</dict>
</plist>
PLIST
plutil -lint "$app/Contents/Info.plist" >/dev/null
echo "$app"
