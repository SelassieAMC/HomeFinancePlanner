// Package migrations embeds the SQL migration files so they ship inside the
// server binary and can be applied at startup by the repository layer.
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
