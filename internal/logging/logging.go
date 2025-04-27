// Package for logging
package logging

import (
	"context"
	"io"
	"log/slog"
)

type ctxKey string

const (
	slogFields ctxKey = "slog_fields"
)

// Struct for the context handler
type ContextHandler struct {
	slog.Handler
}

// Adds contextual attributes to the Record before
// calling the underlying handler
func (h ContextHandler) Handle(ctx context.Context, r slog.Record) error {
	if attrs, ok := ctx.Value(slogFields).([]slog.Attr); ok {
		for _, v := range attrs {
			r.AddAttrs(v)
		}
	}

	return h.Handler.Handle(ctx, r)
}

// Adds an slog attribute to the provided context so that it will be
// included in any Record created with such context
func AppendCtx(parent context.Context, attr slog.Attr) context.Context {
	if parent == nil {
		parent = context.Background()
	}

	if v, ok := parent.Value(slogFields).([]slog.Attr); ok {
		v = append(v, attr)
		return context.WithValue(parent, slogFields, v)
	}

	v := []slog.Attr{}
	v = append(v, attr)
	return context.WithValue(parent, slogFields, v)
}

type nullWriter struct {
	io.Writer
}

func (nullWriter) Write(p []byte) (n int, err error) {
	return len(p), nil
}

func NullLogger() *ContextHandler {
	return &ContextHandler{
		slog.NewJSONHandler(
			nullWriter{},
			&slog.HandlerOptions{},
		),
	}
}
