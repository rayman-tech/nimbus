// Package env provides a way to access environmental dependencies
package env

import (
	"context"
	"log/slog"

	"nimbus/internal/config"
	"nimbus/internal/database"
	"nimbus/internal/logging"
)

type envKeyType struct{}

var envKey envKeyType

// Env holds the dependencies for the environment.
type Env struct {
	Logger   *slog.Logger
	Database database.Querier
	Config   config.Config
}

// Null constructs a null instance.
func Null() *Env {
	return &Env{
		Logger:   slog.New(logging.NullLogger()),
		Database: nil,
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
