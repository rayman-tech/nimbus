// Package database
package database

import (
	"context"
	"fmt"

	"nimbus/internal/sql"
)

type Database struct {
	Querier

	db DBTX
}

func NewDatabase(db DBTX) *Database {
	return &Database{
		Querier: New(db),
		db:      db,
	}
}

// EnsureSchema ensures the database schema is applied to the
// Postgres database. The schema is applied to the database
// if the schema is not detected.
func (db *Database) EnsureSchema(ctx context.Context) error {
	exists, err := db.CheckProjectsTableExists(ctx)
	if err != nil {
		return fmt.Errorf("checking projects table existance: %w", err)
	}

	if exists {
		return nil
	}

	if _, err := db.db.Exec(ctx, sql.Schema()); err != nil {
		return fmt.Errorf("applying database schema: %w", err)
	}

	return nil
}
