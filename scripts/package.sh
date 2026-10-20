#!/usr/bin/env sh
# Packages dist/ as installable Grafana plugin archives, one per platform (the model is compiled
# into the backend binary, so a single multi-platform archive would be several GB):
#   artifacts/consi-timestral-app-<version>.<os>_<arch>.zip
# Unzip into the Grafana plugins directory.
set -eu
ID="consi-timestral-app"
VERSION=$(node -p "require('./package.json').version")
OUT="artifacts"
LICENSE_SRC="pkg/model/weights/LICENSE"

for f in dist/plugin.json dist/module.js dist/NOTICE dist/datasource/plugin.json "$LICENSE_SRC"; do
  [ -e "$f" ] || { echo "missing $f; run npm run build:all" >&2; exit 1; }
done
mkdir -p "$OUT"

found=0
for bin in dist/datasource/gpx_*; do
  name=$(basename "$bin")
  platform=${name#gpx_timestral_ds_}
  platform=${platform%.exe}
  found=1
  stage="$OUT/stage-$platform"
  rm -rf "$stage" && mkdir -p "$stage/$ID"
  # Everything except the other platforms' binaries.
  (cd dist && find . -type f ! -path './datasource/gpx_*') | while read -r f; do
    mkdir -p "$stage/$ID/$(dirname "$f")"
    cp "dist/$f" "$stage/$ID/$f"
  done
  cp "$bin" "$stage/$ID/datasource/"
  mkdir -p "$stage/$ID/model" && cp "$LICENSE_SRC" "$stage/$ID/model/LICENSE"
  zip_name="$ID-$VERSION.$platform.zip"
  (cd "$stage" && rm -f "../$zip_name" && zip -qr -9 "../$zip_name" "$ID")
  rm -rf "$stage"
  (cd "$OUT" && shasum -a 1 "$zip_name" > "$zip_name.sha1" && shasum -a 256 "$zip_name" > "$zip_name.sha256")
  ls -lh "$OUT/$zip_name"
done
[ "$found" = 1 ] || { echo "no backend binaries in dist/datasource; run npm run build:backend" >&2; exit 1; }
