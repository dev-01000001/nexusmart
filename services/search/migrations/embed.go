package migrations

import "embed"

// Files holds all embedded forward-only SQL migrations for the search service.
//
//go:embed *.sql
var Files embed.FS
