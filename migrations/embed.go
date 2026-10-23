// Package migrations contains immutable SQL sources embedded at build time.
package migrations

import (
	"embed"
	"io/fs"
)

//go:embed *.sql
var migrationFS embed.FS

// FS is rooted at the SQL files and does not depend on the working directory.
// Adding a migration requires rebuilding the application.
func FS() fs.FS { return migrationFS }
