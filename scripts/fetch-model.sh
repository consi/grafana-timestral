#!/usr/bin/env sh
# Fetches the Timestral-209M checkpoint that is compiled into the plugin (pkg/model/weights).
# Build-time only. The weights are licensed by Mistral AI under a Modified MIT license, which
# ships with the plugin (model/LICENSE).
set -eu

REPO="mistralai/Timestral-209M"
# Pinned commit of the Hugging Face repo and the SHA-256 of every file.
REVISION="9874d252ab0aae16f7f7c999f98f98d6ad7d8365"
SHA256_config_json="1764139bb6d37ee5ff543f470495c086cfa6c373f7134fc61c7e3d844538bfd8"
SHA256_model_safetensors="1456effb9927e3daa8c7234f10497e3424233869245a9c72b8ecebaf7ad2b7fa"
SHA256_LICENSE="648194727930df730890e9f9affdbcc5c930d406d6829559b25ca3d5a973fc8c"
DEST="${1:-pkg/model/weights}"
CACHE=".model-cache/Timestral-209M-$REVISION"

sha256() { if command -v sha256sum >/dev/null; then sha256sum "$1" | cut -d' ' -f1; else shasum -a 256 "$1" | cut -d' ' -f1; fi; }

mkdir -p "$CACHE" "$DEST"
for f in config.json model.safetensors LICENSE; do
  var="SHA256_$(echo "$f" | tr '.' '_')"
  want=$(eval echo "\$$var")
  if [ ! -s "$CACHE/$f" ] || [ "$(sha256 "$CACHE/$f")" != "$want" ]; then
    echo "Downloading $REPO/$f@$REVISION ..."
    curl -fL --retry 3 -o "$CACHE/$f.part" "https://huggingface.co/$REPO/resolve/$REVISION/$f"
    mv "$CACHE/$f.part" "$CACHE/$f"
  fi
  if [ "$(sha256 "$CACHE/$f")" != "$want" ]; then
    echo "checksum mismatch for $f" >&2
    exit 1
  fi
  # Hard link when possible to avoid a second copy.
  ln -f "$CACHE/$f" "$DEST/$f" 2>/dev/null || cp "$CACHE/$f" "$DEST/$f"
done
echo "Fetched $REPO@$REVISION into $DEST"
