// Package setup contains functions for setting up dependencies and components on startup.
package setup

import (
	"context"
	"fmt"
	"strconv"

	"nimbus/internal/config"
	"nimbus/internal/database"

	"github.com/jackc/pgx/v5/pgxpool"
)

func Database(ctx context.Context, config config.EnvConfig) (*database.Database, error) {
	poolConfig, err := pgxpool.ParseConfig("")
	if err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}

	port, err := strconv.ParseUint(config.Database.Port, 10, 16)
	if err != nil {
		return nil, fmt.Errorf("parsing config port: %w", err)
	}

	poolConfig.ConnConfig.Host = config.Database.Host
	poolConfig.ConnConfig.Port = uint16(port)
	poolConfig.ConnConfig.User = config.Database.User
	poolConfig.ConnConfig.Password = config.Database.Password
	poolConfig.ConnConfig.Database = config.Database.Name

	// Create DB connection
	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		return nil, fmt.Errorf("connecting to db: %w", err)
	}
	db := database.NewDatabase(pool)

	// Apply schema
	err = db.EnsureSchema(ctx)
	if err != nil {
		return nil, fmt.Errorf("ensuring schema: %w", err)
	}

	return db, nil
}
