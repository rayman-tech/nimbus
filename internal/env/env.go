// Package env provides a way to access environmental dependencies
package env

import (
	"context"

	"nimbus/internal/config"
	"nimbus/internal/database"
)

type envKeyType struct{}

var envKey envKeyType

// Env holds the dependencies for the environment.
type Env struct {
	Database database.Querier
	Config   *config.Config
}

func WithContext(ctx context.Context, env *Env) context.Context {
	return context.WithValue(ctx, envKey, env)
}

func FromContext(ctx context.Context) *Env {
	env, ok := ctx.Value(envKey).(*Env)
	if !ok {
		return &Env{}
	}
	return env
}
