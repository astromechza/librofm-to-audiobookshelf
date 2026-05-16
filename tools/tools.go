//go:build tools
// +build tools

// Package tools pins build-time tools so `go mod tidy` keeps them in go.sum.
// Run `go install` directly when needed; this file's purpose is purely to
// anchor versions, not to be compiled into the binary.
package tools

import (
	_ "github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen"
)
