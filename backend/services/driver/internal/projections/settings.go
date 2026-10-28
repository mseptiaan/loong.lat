package projections

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/pkg/eventbus"
	"github.com/mseptiaan/loong.lat/backend/services/driver/internal/settings"
)

// settingsUpdated stores one settings group at its version; an older or repeated version changes nothing. A
// group this service does not know is ignored: back office may announce more than the app reads.
func (p *Projections) settingsUpdated(ctx context.Context, tx pgx.Tx, env contracts.Envelope) error {
	e, err := env.Payload[contracts.SettingsUpdated]()
	if err != nil {
		return eventbus.Permanent(err)
	}
	value, err := settings.Group(e)
	switch {
	case errors.Is(err, settings.ErrUnknownKey):
		p.log.DebugContext(ctx, "settings.updated for an unknown group ignored", "key", e.Key)
		return nil
	case err != nil:
		return eventbus.Permanent(err)
	}
	stored, err := settings.Put(ctx, tx, e.Key, env.AggregateVersion, value, env.OccurredAt.Time)
	if err == nil && !stored {
		p.log.DebugContext(ctx, "stale settings.updated dropped", "key", e.Key, "version", env.AggregateVersion)
	}
	return err
}
