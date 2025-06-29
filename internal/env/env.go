// Package environment provides a way to access environmental dependencies

package env

import (
	"nimbus/internal/database"
	"nimbus/internal/logging"
	"nimbus/internal/models"

	"log/slog"
)

// Holds the dependencies for the environment
type Env struct {
	Logger     *slog.Logger
	Deployment *models.DeployRequest
	Database   *database.Database
}

// Constructs an Env object with the provided parameters
func NewEnvironment(logger *slog.Logger, database *database.Database) *Env {
	if logger == nil {
		logger = slog.New(logging.NullLogger())
	}

	return &Env{
		Logger:     logger,
		Database:   database,
		Deployment: nil,
	}
}

// Constructs a null instance
func Null() *Env {
	return &Env{
		Logger:     slog.New(logging.NullLogger()),
		Database:   nil,
		Deployment: nil,
	}
}
