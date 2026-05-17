// Package abs is the audiobookshelf HTTP client.
//
// The bulk of this package (in gen.go) is GENERATED from
// api/audiobookshelf.openapi.yaml via oapi-codegen. Edit the spec, then run
// `go generate ./...`. Do not edit gen.go by hand — CI will reject any drift.
//
// Hand-written code lives alongside gen.go:
//   - client.go    constructor + Bearer auth + redacting logger
//   - upload.go    multipart POST /api/upload (oapi-codegen's multipart codegen
//                  is awkward enough that we write it ourselves)
//   - discover.go  post-upload polling for the new library item
package abs

//go:generate go tool oapi-codegen -config ../../api/oapi-codegen.yaml -o gen.go ../../api/audiobookshelf.openapi.yaml
