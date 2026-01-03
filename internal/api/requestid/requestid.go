// Package requestid contains utilities for handling the request id.
package requestid

import "context"

type requestIDKeyType struct{}

var requestIDKey requestIDKeyType

// WithCtx injects a given requestID into a context.
func WithCtx(ctx context.Context, requestID uint64) context.Context {
	return context.WithValue(ctx, requestIDKey, requestID)
}

// FromCtx extracts a requestID from a context if it exists.
// If none is found, then 0 is returned.
func FromCtx(ctx context.Context) uint64 {
	if v, ok := ctx.Value(requestIDKey).(uint64); ok {
		return v
	}
	return 0
}
