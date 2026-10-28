// Package settings is driver-api's copy of the admin-controlled switches. Back office owns them and announces
// each group on the compacted loong.settings.v1 topic; this service follows that topic into a table and serves
// settings.get from it. Until a group has been announced, the frontend's defaults apply.
package settings

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/pkg/platform"
)

// App is the frontend's AppSettings.
type App struct {
	AI       contracts.AIFeatures       `json:"ai"`
	Dispatch contracts.DispatchSettings `json:"dispatch"`
}

// Defaults is the frontend's DEFAULT_SETTINGS: everything on.
var Defaults = App{
	AI:       contracts.AIFeatures{Vision: true, Voice: true, Intent: true, Copilot: true},
	Dispatch: contracts.DispatchSettings{Auto: true},
}

// Put stores the group key at version, unless the table has that version or a newer one already. It reports
// whether the value was stored. value is the group's contract value (AIFeatures or DispatchSettings).
func Put(ctx context.Context, q platform.Querier, key string, version int64, value any, at time.Time) (bool, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return false, fmt.Errorf("settings: encode %s: %w", key, err)
	}
	tag, err := q.Exec(ctx,
		`INSERT INTO settings (key, value, version, updated_at) VALUES ($1, $2, $3, $4)
		 ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, version = EXCLUDED.version, updated_at = EXCLUDED.updated_at
		 WHERE settings.version < EXCLUDED.version`, key, raw, version, at)
	if err != nil {
		return false, fmt.Errorf("settings: store %s: %w", key, err)
	}
	return tag.RowsAffected() == 1, nil
}

// DB is what Read needs: the pool or a transaction.
type DB interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// Read is the stored settings; a group that was never announced has its default.
func Read(ctx context.Context, db DB) (App, error) {
	rows, err := db.Query(ctx, `SELECT key, value FROM settings`)
	if err != nil {
		return App{}, fmt.Errorf("settings: read: %w", err)
	}
	defer rows.Close()
	app := Defaults
	for rows.Next() {
		var (
			key   string
			value []byte
		)
		if err := rows.Scan(&key, &value); err != nil {
			return App{}, fmt.Errorf("settings: scan: %w", err)
		}
		switch key {
		case contracts.SettingsKeyAI:
			err = json.Unmarshal(value, &app.AI)
		case contracts.SettingsKeyDispatch:
			err = json.Unmarshal(value, &app.Dispatch)
		}
		if err != nil {
			return App{}, fmt.Errorf("settings: decode %s: %w", key, err)
		}
	}
	return app, rows.Err()
}

// Service answers settings.get.
type Service struct {
	db DB
	// aiConfigured says the AI gateway is set up (AI_URL): without it every AI flag reads off, so that screens do
	// not offer what is bound to fail.
	aiConfigured bool
}

func NewService(db DB, aiConfigured bool) *Service {
	return &Service{db: db, aiConfigured: aiConfigured}
}

// Get is settings.get: what the admin switched, with the AI flags off unless the gateway exists.
func (s *Service) Get(ctx context.Context) (App, error) {
	app, err := Read(ctx, s.db)
	if err != nil {
		return App{}, err
	}
	if !s.aiConfigured {
		app.AI = contracts.AIFeatures{}
	}
	return app, nil
}

// Mount registers settings.get for any signed-in user (the app reads it, as the mock allows every role).
func (s *Service) Mount(rpc *platform.RPC) {
	rpc.Handle("settings.get", func(ctx context.Context, _ struct{}) (App, error) {
		if _, err := platform.RequireRole(ctx, contracts.RoleCustomer, contracts.RoleAdmin, contracts.RoleDriver); err != nil {
			return App{}, err
		}
		return s.Get(ctx)
	})
}

// Group returns the value an event carries for its key, or an error when the key is unknown or the value is
// missing (an event that can never be applied).
func Group(e contracts.SettingsUpdated) (any, error) {
	switch e.Key {
	case contracts.SettingsKeyAI:
		if e.AI != nil {
			return *e.AI, nil
		}
	case contracts.SettingsKeyDispatch:
		if e.Dispatch != nil {
			return *e.Dispatch, nil
		}
	default:
		return nil, ErrUnknownKey
	}
	return nil, errors.New("settings.updated without the value of " + e.Key)
}

// ErrUnknownKey means the event names a settings group this service does not know; later versions of back
// office may add some, which this one ignores.
var ErrUnknownKey = errors.New("settings: unknown group")
