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

	apierror "nimbus/internal/api/error"
	"nimbus/internal/api/requestid"
	"nimbus/internal/database"
	"nimbus/internal/env"
	"nimbus/internal/logging"

	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/jackc/pgx/v5"
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

				requestID := fmt.Sprintf("%d", requestid.FromContext(r.Context()))

				slog.ErrorContext(r.Context(),
					"panic recovered",
					"panic", rvr,
					"stack", string(debug.Stack()))

				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusInternalServerError)
				_ = json.NewEncoder(w).Encode(&apierror.Error{
					Code:    apierror.InternalServerError,
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
			r = r.WithContext(env.WithContext(r.Context(), e))
			next.ServeHTTP(w, r)
		})
	}
}

func LogRequest(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		requestID := ulid.Now()
		ctx := requestid.WithContext(r.Context(), requestID)
		ctx = logging.With(ctx,
			"request_id", requestID,
			"method", r.Method,
			"path", r.URL.RequestURI(),
		)
		r = r.WithContext(ctx)

		lrw := &logResponseWriter{w, http.StatusOK}
		next.ServeHTTP(lrw, r)

		slog.InfoContext(r.Context(), "request completed",
			"duration", time.Since(start),
			"status", lrw.statusCode,
		)
	})
}

// Authenticate reads the X-API-Key header, looks up the user, and injects the
// user into the request context. This runs as a standard mux middleware so the
// modified request is properly propagated to downstream handlers.
//
// The oapi-codegen nethttp-middleware (v1.1.2) passes the original *http.Request
// to the next handler after validation, discarding any context modifications made
// inside the AuthenticationFunc. By performing user injection here instead, the
// user context is guaranteed to reach the handler.
func Authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		apiKey := r.Header.Get("X-API-Key")
		if apiKey == "" {
			next.ServeHTTP(w, r)
			return
		}

		e := env.FromContext(r.Context())
		user, err := e.Database.GetUserByApiKey(r.Context(), apiKey)
		if err != nil {
			next.ServeHTTP(w, r)
			return
		}

		r = r.WithContext(database.UserWithContext(r.Context(), &user))
		next.ServeHTTP(w, r)
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
	requestID := fmt.Sprintf("%d", requestid.FromContext(r.Context()))

	var errBody *apierror.Error
	if errors.As(err, &errBody) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(opts.StatusCode)
		_ = json.NewEncoder(w).Encode(errBody) //nolint:errchkjson
		return
	}

	if opts.StatusCode >= 400 && opts.StatusCode < 500 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(opts.StatusCode)
		_ = json.NewEncoder(w).Encode(&apierror.Error{ //nolint:errchkjson
			Code:    apierror.BadRequest,
			Status:  opts.StatusCode,
			Message: err.Error(),
			ErrorID: requestID,
		})
		return
	}

	_ = apierror.EncodeInternalError(w, requestID)
}

// OAPIAuthFunc is the authentication function for oapi-codegen middleware.
func OAPIAuthFunc(ctx context.Context, input *openapi3filter.AuthenticationInput) error {
	switch input.SecuritySchemeName {
	case "ApiKeyAuth":
	default:
		return nil
	}

	e := env.FromContext(ctx)
	requestID := fmt.Sprintf("%d", requestid.FromContext(ctx))

	apiKey := input.RequestValidationInput.Request.Header.Get("X-API-Key")
	if apiKey == "" {
		slog.ErrorContext(ctx, "user is missing api key header")
		return &apierror.Error{
			Code:    apierror.InvalidAPIKey,
			Status:  apierror.InvalidAPIKey.Status(),
			Message: "missing header X-API-Key",
			ErrorID: requestID,
		}
	}

	slog.DebugContext(ctx, "checking api key existence")
	_, err := e.Database.GetUserByApiKey(ctx, apiKey)
	if errors.Is(err, pgx.ErrNoRows) {
		slog.ErrorContext(ctx, "api key does not exist", "error", err)
		return &apierror.Error{
			Code:    apierror.InvalidAPIKey,
			Status:  apierror.InvalidAPIKey.Status(),
			Message: "invalid api key",
			ErrorID: requestID,
		}
	} else if err != nil {
		slog.DebugContext(ctx, "failed to check existence", "error", err)
		return &apierror.Error{
			Code:    apierror.InternalServerError,
			Status:  apierror.InternalServerError.Status(),
			Message: "Internal Server Error",
			ErrorID: requestID,
		}
	}

	return nil
}
