// Package api contains functions for the nimbus api.
package api

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"nimbus/docs"
	"nimbus/internal/api/middleware"
	"nimbus/internal/api/openapi"
	"nimbus/internal/api/requestid"
	"nimbus/internal/env"
	"nimbus/internal/metrics"

	apierror "nimbus/internal/api/error"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/gorilla/mux"
	oapimw "github.com/oapi-codegen/nethttp-middleware"
)

func Start(ctx context.Context, port string, env *env.Env) error {
	server := openapi.NewServer()
	spec, err := docs.Docs.ReadFile("api.yaml")
	if err != nil {
		return fmt.Errorf("reading openapi spec: %w", err)
	}
	swagger, err := openapi3.NewLoader().LoadFromData(spec)
	if err != nil {
		return fmt.Errorf("creating openapi loader: %w", err)
	}
	swagger.Servers = nil

	router := mux.NewRouter()
	router.Use(metrics.Middleware)
	router.Use(middleware.InjectEnvironment(env))
	router.Use(middleware.Recover)
	router.Use(middleware.LogRequest)
	router.Use(middleware.Authenticate)
	router.Use(oapimw.OapiRequestValidatorWithOptions(swagger, &oapimw.Options{
		Options: openapi3filter.Options{
			AuthenticationFunc: middleware.OAPIAuthFunc,
		},
		ErrorHandlerWithOpts: middleware.OAPIErrorHandler,
	}))

	// Customize strict handler to return errors in custom format
	strictHandlerOptions := openapi.StrictHTTPServerOptions{
		RequestErrorHandlerFunc: func(w http.ResponseWriter, r *http.Request, err error) {
			requestID := fmt.Sprintf("%d", requestid.FromContext(r.Context()))
			// Request decoding errors are client errors (invalid JSON, etc.)
			_ = apierror.EncodeError(w, apierror.BadRequest, err.Error(), requestID)
		},
		ResponseErrorHandlerFunc: func(w http.ResponseWriter, r *http.Request, err error) {
			requestID := fmt.Sprintf("%d", requestid.FromContext(r.Context()))
			// Response encoding errors are server errors
			_ = apierror.EncodeInternalError(w, requestID)
		},
	}

	handler := openapi.HandlerFromMux(
		openapi.NewStrictHandlerWithOptions(server, nil, strictHandlerOptions),
		router,
	)

	// Serve metrics on a parent mux so the Prometheus endpoint bypasses the
	// OpenAPI request validator (which would otherwise reject /metrics as an
	// unknown route). Everything else falls through to the API handler.
	root := http.NewServeMux()
	root.Handle("/metrics", metrics.Handler())
	root.Handle("/", handler)

	s := &http.Server{
		Handler: root,
		Addr:    "0.0.0.0:" + port,
	}

	errCh := make(chan error, 1)
	go func() {
		slog.Info("server listening", "address", s.Addr)
		errCh <- s.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		slog.Info("shutting down server")
		const shutdownTimeout = 10 * time.Second
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		return s.Shutdown(shutdownCtx)
	}
}
