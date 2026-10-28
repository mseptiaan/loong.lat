// Package pg holds the small pgx helpers the domain packages share.
package pg

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Querier is what a pool and a transaction have in common, so a store function runs in the caller's
// transaction or on the pool alike. (platform.Querier lacks Query.)
type Querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// UniqueViolation reports whether err is a unique-constraint violation, and which constraint it broke.
func UniqueViolation(err error) (constraint string, ok bool) {
	if pgErr, found := errors.AsType[*pgconn.PgError](err); found && pgErr.Code == "23505" {
		return pgErr.ConstraintName, true
	}
	return "", false
}
