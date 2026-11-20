package migrations

import "embed"

// FS holds embedded SQL migration files for the shipping service.
//
//go:embed *.sql
var FS embed.FS
