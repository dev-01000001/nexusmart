package migrations

import "embed"

// Files holds all embedded SQL migration files for the user service.
//
//go:embed *.sql
var Files embed.FS
