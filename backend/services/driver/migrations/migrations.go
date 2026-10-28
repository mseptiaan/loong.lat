// Package migrations holds the DDL of driver-api's own tables.
package migrations

import (
	"embed"

	"github.com/mseptiaan/loong.lat/backend/pkg/platform"
)

//go:embed *.sql
var files embed.FS

// All returns the migrations in order, to be applied after the platform's and the eventbus's.
func All() ([]platform.Migration, error) {
	return platform.MigrationsFromFS("driver", files)
}
