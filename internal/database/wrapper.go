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

	if !exists {
		if _, err := db.db.Exec(ctx, sql.Schema()); err != nil {
			return fmt.Errorf("applying database schema: %w", err)
		}
		return nil
	}

	if err := db.applyMigrations(ctx); err != nil {
		return fmt.Errorf("applying migrations: %w", err)
	}

	return nil
}

// applyMigrations applies incremental schema changes to an existing database.
func (db *Database) applyMigrations(ctx context.Context) error {
	// Add commit_hash column to services table if it doesn't exist.
	const addCommitHash = `
		ALTER TABLE services ADD COLUMN IF NOT EXISTS commit_hash text NULL
	`
	if _, err := db.db.Exec(ctx, addCommitHash); err != nil {
		return fmt.Errorf("adding commit_hash column: %w", err)
	}

	return nil
}
