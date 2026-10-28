// Package migrations embeds console-api's own schema. Each file is applied once, in name order, after the
// platform and eventbus tables (see platform.Migrate).
package migrations

import (
	"embed"

	"github.com/mseptiaan/loong.lat/backend/pkg/platform"
)

//go:embed *.sql
var files embed.FS

// All returns the console's migrations in the order they must run.
func All() ([]platform.Migration, error) {
	return platform.MigrationsFromFS("console", files)
}
