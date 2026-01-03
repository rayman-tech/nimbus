// Package middleware contains api middleware.
package middleware

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"
	"time"

	apiError "nimbus/internal/api/error"
	"nimbus/internal/api/requestid"
	"nimbus/internal/env"
	"nimbus/internal/logging"

	oapimw "github.com/oapi-codegen/nethttp-middleware"
	"github.com/oklog/ulid/v2"
)

// logResponseWriter captures the status code.
type logResponseWriter struct {
	http.ResponseWriter

	statusCode int
}

// Captures the status code and writes the response.
func (lrw *logResponseWriter) WriteHeader(code int) {
	lrw.statusCode = code
	lrw.ResponseWriter.WriteHeader(code)
}

func Recover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rvr := recover(); rvr != nil {
				if err, ok := rvr.(error); ok && errors.Is(err, http.ErrAbortHandler) {
					panic(rvr)
				}

				e := env.FromContext(r.Context())
				requestID := fmt.Sprintf("%d", requestid.FromCtx(r.Context()))

				e.Logger.ErrorContext(r.Context(),
					"panic recovered",
					slog.Any("panic", rvr),
					slog.String("stack", string(debug.Stack())))

				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusInternalServerError)
				_ = json.NewEncoder(w).Encode(&apiError.Error{
					Code:    apiError.InternalServerError,
					Status:  http.StatusInternalServerError,
					Message: "internal server error",
					ErrorID: requestID,
				})
			}
		}()

		next.ServeHTTP(w, r)
	})
}

func InjectEnvironment(e *env.Env) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if e == nil {
				e = env.Null()
			}
			r = r.WithContext(env.WithContext(r.Context(), e))
			next.ServeHTTP(w, r)
		})
	}
}

func LogRequest(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		e := env.FromContext(r.Context())

		ctx := r.Context()
		requestID := ulid.Now()
		r = r.WithContext(requestid.WithCtx(r.Context(), requestID))
		r = r.WithContext(logging.AppendCtx(ctx, slog.Uint64("request_id", requestID)))
		r = r.WithContext(logging.AppendCtx(r.Context(), slog.String("method", r.Method)))
		r = r.WithContext(logging.AppendCtx(r.Context(), slog.String("path", r.URL.RequestURI())))
		lrw := &logResponseWriter{w, http.StatusOK}
		next.ServeHTTP(lrw, r)
		e.Logger.LogAttrs(
			r.Context(),
			slog.LevelInfo,
			"Request completed",
			slog.Duration("duration", time.Since(start)),
			slog.Int("status", lrw.statusCode),
		)
	})
}

// OAPIErrorHandler handles errors from oapi-codegen middleware and formats them
// according to your error schema.
func OAPIErrorHandler(
	ctx context.Context,
	err error,
	w http.ResponseWriter,
	r *http.Request,
	opts oapimw.ErrorHandlerOpts,
) {
	// Several scenarios where we are handling an error:
	//   1. apiError returned from middleware/handler
	//   2. validation error
	//   3. fallback - internal server error

	requestID := fmt.Sprintf("%d", requestid.FromCtx(r.Context()))

	// 1. apiError
	var errBody *apiError.Error
	if errors.As(err, &errBody) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(opts.StatusCode)
		_ = json.NewEncoder(w).Encode(errBody) //nolint:errchkjson
		return
	}

	// 2. Validation error
	if opts.StatusCode >= 400 && opts.StatusCode < 500 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(opts.StatusCode)
		_ = json.NewEncoder(w).Encode(&apiError.Error{ //nolint:errchkjson
			Code:    apiError.BadRequest,
			Status:  opts.StatusCode,
			Message: err.Error(),
			ErrorID: requestID,
		})
		return
	}

	// 3. internal server error
	_ = apiError.EncodeInternalError(w, requestID)
}
