package migrations

import "embed"

// Files contains the ordered, immutable database migrations shipped with the server.
//
//go:embed *.sql
var Files embed.FS
