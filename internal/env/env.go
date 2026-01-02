// Package env provides a way to access environmental dependencies
package env

import (
	"context"
	"log/slog"

	"nimbus/internal/config"
	"nimbus/internal/database"
	"nimbus/internal/logging"
	"nimbus/internal/models"
)

type envKeyType struct{}

var envKey envKeyType

// Env holds the dependencies for the environment.
type Env struct {
	Logger     *slog.Logger
	Deployment *models.DeployRequest
	Database   *database.Database
	Config     config.Config
}

// NewEnvironment constructs an Env object with the provided parameters.
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

// Null constructs a null instance.
func Null() *Env {
	return &Env{
		Logger:     slog.New(logging.NullLogger()),
		Database:   nil,
		Deployment: nil,
	}
}

func WithContext(ctx context.Context, env *Env) context.Context {
	return context.WithValue(ctx, envKey, env)
}

func FromContext(ctx context.Context) *Env {
	env, ok := ctx.Value(envKey).(*Env)
	if !ok {
		return Null()
	}
	return env
}
