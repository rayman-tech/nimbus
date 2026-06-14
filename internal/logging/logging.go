// Package logging provides context-enriched structured logging.
//
// Call Init() once at startup to configure the global slog logger.
// Use With() to attach key-value attributes to a context. Any slog call
// using that context (e.g. slog.InfoContext) will automatically include
// those attributes via the custom contextHandler.
package logging

import (
	"context"
	"log/slog"
	"os"
	"strings"
)

type attrsKey struct{}

// Init configures the global slog logger with a context-aware handler.
func Init(level string) {
	inner := slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{
		Level: parseLevel(level),
	})
	slog.SetDefault(slog.New(&contextHandler{inner: inner}))
}

// With adds structured key-value pairs to the context. Any slog call using
// this context will automatically include these attributes.
func With(ctx context.Context, args ...any) context.Context {
	existing := fromContext(ctx)
	merged := make([]any, len(existing), len(existing)+len(args))
	copy(merged, existing)
	merged = append(merged, args...)
	return context.WithValue(ctx, attrsKey{}, merged)
}

func fromContext(ctx context.Context) []any {
	if v, ok := ctx.Value(attrsKey{}).([]any); ok {
		return v
	}
	return nil
}

// contextHandler is a slog.Handler that injects context-stored attributes
// into every log record.
type contextHandler struct {
	inner slog.Handler
}

func (h *contextHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.inner.Enabled(ctx, level)
}

func (h *contextHandler) Handle(ctx context.Context, r slog.Record) error {
	if args := fromContext(ctx); len(args) > 0 {
		for i := 0; i+1 < len(args); i += 2 {
			if key, ok := args[i].(string); ok {
				r.AddAttrs(slog.Any(key, args[i+1]))
			}
		}
	}
	return h.inner.Handle(ctx, r)
}

func (h *contextHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &contextHandler{inner: h.inner.WithAttrs(attrs)}
}

func (h *contextHandler) WithGroup(name string) slog.Handler {
	return &contextHandler{inner: h.inner.WithGroup(name)}
}

func parseLevel(level string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
