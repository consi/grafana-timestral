//go:build mage
// +build mage

package main

import (
	// mage:import
	build "github.com/grafana/grafana-plugin-sdk-go/build"
)

// Default builds the backend (the nested data source in src/datasource) for every platform.
// The model must be fetched first (scripts/fetch-model.sh): it is compiled into the binary.
var Default = build.BuildAll
