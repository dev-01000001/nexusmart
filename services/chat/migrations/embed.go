package migrations

import "embed"

// FS holds embedded SQL migration files for the chat service.
//
//go:embed *.sql
var FS embed.FS
