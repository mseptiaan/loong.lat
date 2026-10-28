// Package settings holds the admin-controlled switches (AI features, automatic dispatch). Each group is
// announced on the compacted settings topic, so the other services follow without asking.
package settings

import (
	"context"
	"encoding/json/v2"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/pkg/eventbus"
	"github.com/mseptiaan/loong.lat/backend/pkg/platform"
	"github.com/mseptiaan/loong.lat/backend/services/backoffice/internal/apix"
	"github.com/mseptiaan/loong.lat/backend/services/backoffice/internal/changes"
	"github.com/mseptiaan/loong.lat/backend/services/backoffice/internal/pg"
)

// App is the frontend's AppSettings. The defaults are the rows the migration inserts.
type App struct {
	AI       contracts.AIFeatures       `json:"ai"`
	Dispatch contracts.DispatchSettings `json:"dispatch"`
}

// Patch is the argument of settings.update: groups and fields left out are not changed.
type Patch struct {
	AI       *AIPatch       `json:"ai"`
	Dispatch *DispatchPatch `json:"dispatch"`
}

type AIPatch struct {
	Vision  *bool `json:"vision"`
	Voice   *bool `json:"voice"`
	Intent  *bool `json:"intent"`
	Copilot *bool `json:"copilot"`
}

type DispatchPatch struct {
	Auto *bool `json:"auto"`
}

func set(dst *bool, src *bool) {
	if src != nil {
		*dst = *src
	}
}

func (p Patch) apply(s App) App {
	if p.AI != nil {
		set(&s.AI.Vision, p.AI.Vision)
		set(&s.AI.Voice, p.AI.Voice)
		set(&s.AI.Intent, p.AI.Intent)
		set(&s.AI.Copilot, p.AI.Copilot)
	}
	if p.Dispatch != nil {
		set(&s.Dispatch.Auto, p.Dispatch.Auto)
	}
	return s
}

// Service reads and changes the settings.
type Service struct {
	db   *pgxpool.Pool
	pub  *eventbus.Publisher
	feed *changes.Feed
	// onAutoEnabled runs after automatic dispatch is switched on, so waiting plans are served at once.
	onAutoEnabled func()
}

func New(db *pgxpool.Pool, pub *eventbus.Publisher, feed *changes.Feed, onAutoEnabled func()) *Service {
	return &Service{db: db, pub: pub, feed: feed, onAutoEnabled: onAutoEnabled}
}

// Mount registers settings.get and settings.update, both for admins.
func (s *Service) Mount(rpc *platform.RPC) {
	type updateIn struct {
		Patch Patch `json:"patch"`
	}
	rpc.Handle("settings.get", apix.Admin(func(ctx context.Context, _ struct{}) (App, error) {
		return Read(ctx, s.db)
	}))
	rpc.Handle("settings.update", apix.Admin(func(ctx context.Context, in updateIn) (App, error) {
		return s.Update(ctx, in.Patch)
	}))
}

// Read returns the settings as of now.
func Read(ctx context.Context, q pg.Querier) (App, error) {
	app, _, err := read(ctx, q, false)
	return app, err
}

// DispatchAuto reports whether plans are assigned to drivers automatically.
func DispatchAuto(ctx context.Context, q pg.Querier) (bool, error) {
	app, err := Read(ctx, q)
	return app.Dispatch.Auto, err
}

// read loads both groups with their versions, locking the rows until the transaction ends when lock is set.
func read(ctx context.Context, q pg.Querier, lock bool) (App, map[string]int64, error) {
	query := `SELECT key, value, version FROM settings ORDER BY key`
	if lock {
		query += ` FOR UPDATE`
	}
	rows, err := q.Query(ctx, query)
	if err != nil {
		return App{}, nil, fmt.Errorf("settings: read: %w", err)
	}
	defer rows.Close()
	var app App
	versions := map[string]int64{}
	for rows.Next() {
		var key string
		var value []byte
		var version int64
		if err := rows.Scan(&key, &value, &version); err != nil {
			return App{}, nil, fmt.Errorf("settings: scan: %w", err)
		}
		versions[key] = version
		switch key {
		case contracts.SettingsKeyAI:
			err = json.Unmarshal(value, &app.AI)
		case contracts.SettingsKeyDispatch:
			err = json.Unmarshal(value, &app.Dispatch)
		}
		if err != nil {
			return App{}, nil, fmt.Errorf("settings: decode %s: %w", key, err)
		}
	}
	return app, versions, rows.Err()
}

// Update applies the patch. Each group that actually changes is stored and announced as one settings.updated
// event keyed by the group; a patch that changes nothing announces nothing.
func (s *Service) Update(ctx context.Context, patch Patch) (App, error) {
	var cur, next App
	err := platform.WithTx(ctx, s.db, func(tx pgx.Tx) error {
		var versions map[string]int64
		var err error
		if cur, versions, err = read(ctx, tx, true); err != nil {
			return err
		}
		next = patch.apply(cur)
		if next.AI != cur.AI {
			ev := contracts.SettingsUpdated{Key: contracts.SettingsKeyAI, AI: &next.AI}
			if err := s.store(ctx, tx, contracts.SettingsKeyAI, versions[contracts.SettingsKeyAI]+1, next.AI, ev); err != nil {
				return err
			}
		}
		if next.Dispatch != cur.Dispatch {
			ev := contracts.SettingsUpdated{Key: contracts.SettingsKeyDispatch, Dispatch: &next.Dispatch}
			return s.store(ctx, tx, contracts.SettingsKeyDispatch, versions[contracts.SettingsKeyDispatch]+1, next.Dispatch, ev)
		}
		return nil
	})
	if err != nil {
		return cur, err
	}
	if next == cur {
		return next, nil
	}
	s.feed.Settings()
	if !cur.Dispatch.Auto && next.Dispatch.Auto {
		s.onAutoEnabled()
	}
	return next, nil
}

// store writes one group at its new version and enqueues the event that announces it.
func (s *Service) store(ctx context.Context, tx pgx.Tx, key string, version int64, value any, ev contracts.SettingsUpdated) error {
	body, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("settings: encode %s: %w", key, err)
	}
	if _, err := tx.Exec(ctx, `UPDATE settings SET value = $2, version = $3, updated_at = now() WHERE key = $1`, key, body, version); err != nil {
		return fmt.Errorf("settings: write %s: %w", key, err)
	}
	return s.pub.Enqueue(ctx, tx, eventbus.Event{Type: contracts.TypeSettingsUpdated, Key: key, AggregateVersion: version, Data: ev})
}
