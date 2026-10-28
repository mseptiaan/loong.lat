// Package settings is the console's copy of the app-wide settings back office publishes (loong.settings.v1,
// compacted: the last record of each key is its value) and the settings.get that serves them to the dashboard.
package settings

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/pkg/eventbus"
	"github.com/mseptiaan/loong.lat/backend/pkg/platform"
	"github.com/mseptiaan/loong.lat/backend/services/console/internal/notify"
)

// App is the frontend's AppSettings.
type App struct {
	AI       contracts.AIFeatures       `json:"ai"`
	Dispatch contracts.DispatchSettings `json:"dispatch"`
}

// Default is DEFAULT_SETTINGS of the frontend: everything on. It is what a key that never arrived reads as.
var Default = App{
	AI:       contracts.AIFeatures{Vision: true, Voice: true, Intent: true, Copilot: true},
	Dispatch: contracts.DispatchSettings{Auto: true},
}

// Projection holds the settings seen on the settings topic.
type Projection struct {
	pool *pgxpool.Pool
	// aiAvailable says an AI gateway is configured. Without one no AI feature can work, so none is offered
	// whatever the admin chose: a screen must not offer what is certain to fail.
	aiAvailable bool
}

func New(pool *pgxpool.Pool, aiAvailable bool) *Projection {
	return &Projection{pool: pool, aiAvailable: aiAvailable}
}

// Register handles the settings topic. Each event carries the whole value of one key and replaces what was
// stored, unless the stored one is newer by aggregate version (a redelivery after a newer record).
func (p *Projection) Register(c *eventbus.Consumer) {
	c.Handle(contracts.TypeSettingsUpdated, update)
}

func update(ctx context.Context, tx pgx.Tx, env contracts.Envelope) error {
	ev, err := env.Payload[contracts.SettingsUpdated]()
	if err != nil {
		return eventbus.Permanent(err)
	}
	var value any
	switch ev.Key {
	case contracts.SettingsKeyAI:
		if ev.AI == nil {
			return eventbus.Permanent(errors.New("settings.updated for ai without a value"))
		}
		value = ev.AI
	case contracts.SettingsKeyDispatch:
		if ev.Dispatch == nil {
			return eventbus.Permanent(errors.New("settings.updated for dispatch without a value"))
		}
		value = ev.Dispatch
	default:
		return nil // a key a newer back office added: nothing here reads it
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return eventbus.Permanent(err)
	}
	_, err = tx.Exec(ctx,
		`INSERT INTO settings (key, value, version, updated_at) VALUES ($1, $2, $3, $4)
		 ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, version = EXCLUDED.version, updated_at = EXCLUDED.updated_at
		  WHERE settings.version <= EXCLUDED.version`,
		ev.Key, raw, env.AggregateVersion, env.OccurredAt.Time)
	if err != nil {
		return err
	}
	return notify.SettingsChanged(ctx, tx)
}

// Get is the settings as the dashboard sees them.
func (p *Projection) Get(ctx context.Context) (App, error) {
	rows, err := p.pool.Query(ctx, `SELECT key, value FROM settings`)
	if err != nil {
		return App{}, err
	}
	defer rows.Close()
	app := Default
	for rows.Next() {
		var key string
		var raw []byte
		if err := rows.Scan(&key, &raw); err != nil {
			return App{}, err
		}
		switch key {
		case contracts.SettingsKeyAI:
			err = json.Unmarshal(raw, &app.AI)
		case contracts.SettingsKeyDispatch:
			err = json.Unmarshal(raw, &app.Dispatch)
		}
		if err != nil {
			return App{}, fmt.Errorf("settings: stored %s: %w", key, err)
		}
	}
	if err := rows.Err(); err != nil {
		return App{}, err
	}
	if !p.aiAvailable {
		app.AI = contracts.AIFeatures{}
	}
	return app, nil
}

// Mount adds settings.get, which any signed-in user may call.
func (p *Projection) Mount(r *platform.RPC) {
	r.Handle("settings.get", func(ctx context.Context, _ struct{}) (App, error) {
		if _, err := platform.RequireRole(ctx, contracts.RoleCustomer, contracts.RoleAdmin, contracts.RoleDriver); err != nil {
			return App{}, err
		}
		return p.Get(ctx)
	})
}
