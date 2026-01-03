package api

import (
	"fmt"
	"log/slog"
	"net/http"
	"nimbus/docs"
	"nimbus/internal/api/middleware"
	"nimbus/internal/api/openapi"
	"nimbus/internal/api/requestid"
	"nimbus/internal/env"

	apiError "nimbus/internal/api/error"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/gorilla/mux"
	oapimw "github.com/oapi-codegen/nethttp-middleware"
)

func Start(port string, env *env.Env) error {
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
	router.Use(middleware.InjectEnvironment(env))
	router.Use(middleware.Recover)
	router.Use(middleware.LogRequest)
	router.Use(oapimw.OapiRequestValidatorWithOptions(swagger, &oapimw.Options{
		Options: openapi3filter.Options{
			AuthenticationFunc: middleware.OAPIAuthFunc,
		},
		ErrorHandlerWithOpts: middleware.OAPIErrorHandler,
	}))

	// Customize strict handler to return errors in custom format
	strictHandlerOptions := openapi.StrictHTTPServerOptions{
		RequestErrorHandlerFunc: func(w http.ResponseWriter, r *http.Request, err error) {
			requestID := fmt.Sprintf("%d", requestid.FromCtx(r.Context()))
			// Request decoding errors are client errors (invalid JSON, etc.)
			_ = apiError.EncodeError(w, apiError.BadRequest, err.Error(), requestID)
		},
		ResponseErrorHandlerFunc: func(w http.ResponseWriter, r *http.Request, err error) {
			requestID := fmt.Sprintf("%d", requestid.FromCtx(r.Context()))
			// Response encoding errors are server errors
			_ = apiError.EncodeInternalError(w, requestID)
		},
	}

	handler := openapi.HandlerFromMux(
		openapi.NewStrictHandlerWithOptions(server, nil, strictHandlerOptions),
		router,
	)
	s := &http.Server{
		Handler: handler,
		Addr:    "0.0.0.0:" + port,
	}

	env.Logger.Info("server listening", slog.String("address", "0.0.0.0:"+port))
	return s.ListenAndServe()
}
