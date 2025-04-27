// Package environment provides a way to access environmental dependencies

package env

import (
	"log/slog"
	"nimbus/internal/database"
	"nimbus/internal/logging"
	"nimbus/internal/registrar"
)

// Holds the dependencies for the environment
type Env struct {
	*slog.Logger
	*registrar.Registrar
	*database.Database
}

// Gets the value stored in the registrar for the given key
// and converts it to a string.
func (e *Env) Getenv(key string) string {
	if e == nil {
		return ""
	}

	if value, ok := e.Registrar.Get(key).(string); ok {
		return value
	}
	return ""
}

// Constructs an Env object with the provided parameters
func Environment(logger *slog.Logger, registrar *registrar.Registrar, database *database.Database) *Env {
	if logger == nil {
		logger = slog.New(logging.NullLogger())
	}

	return &Env{
		Logger:    logger,
		Registrar: registrar,
		Database:  database,
	}
}

// Constructs a null instance
func Null() *Env {
	return &Env{
		Logger:    slog.New(logging.NullLogger()),
		Registrar: nil,
		Database:  nil,
	}
}
