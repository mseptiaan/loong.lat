// Package migrations holds backoffice-api's own DDL.
package migrations

import (
	"embed"

	"github.com/mseptiaan/loong.lat/backend/pkg/platform"
)

//go:embed *.sql
var files embed.FS

// All returns the service's migrations in order, to be applied after the platform's and the eventbus's.
func All() ([]platform.Migration, error) {
	return platform.MigrationsFromFS("backoffice", files)
}
