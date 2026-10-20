// Package model embeds the Timestral-209M checkpoint into the plugin binary, so the plugin
// needs no file system access to load it. The files are fetched at build time by
// scripts/fetch-model.sh (checksum-verified) into pkg/model/weights.
package model

import _ "embed"

// ID is the embedded checkpoint.
const ID = "mistralai/Timestral-209M"

// Config is the checkpoint's config.json.
//
//go:embed weights/config.json
var Config []byte

// Weights is the checkpoint's model.safetensors (read-only; tensors alias it).
//
//go:embed weights/model.safetensors
var Weights []byte
