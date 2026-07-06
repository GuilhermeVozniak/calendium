// Package migrations embeds the SQL migration files so cmd/api and
// cmd/worker can apply them at boot via internal/migrate.
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
