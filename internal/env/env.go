// Package environment provides a way to access environmental dependencies

package env

import (
	"log/slog"
	"nimbus/internal/database"
	"nimbus/internal/logging"
)

// Holds the dependencies for the environment
type Env struct {
	*slog.Logger
	*database.Database
}

// Constructs an Env object with the provided parameters
func NewEnvironment(logger *slog.Logger, database *database.Database) *Env {
	if logger == nil {
		logger = slog.New(logging.NullLogger())
	}

	return &Env{
		Logger:   logger,
		Database: database,
	}
}

// Constructs a null instance
func Null() *Env {
	return &Env{
		Logger:   slog.New(logging.NullLogger()),
		Database: nil,
	}
}
