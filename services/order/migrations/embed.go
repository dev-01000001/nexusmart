package migrations

import "embed"

// Files holds all embedded SQL migrations for order_db.
//
//go:embed *.sql
var Files embed.FS
