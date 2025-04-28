package api

import (
	"nimbus/internal/api/handlers"
	"nimbus/internal/database"
	nimbusEnv "nimbus/internal/env"
	"nimbus/internal/logging"
	"nimbus/internal/registrar"

	"context"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/gorilla/mux"
	"github.com/jackc/pgx/v5"
)

const envKey = "env"

// Custom ResponseWriter that captures the status code
type logResponseWriter struct {
	http.ResponseWriter
	statusCode int
}

// Captures the status code and writes the response
func (lrw *logResponseWriter) WriteHeader(code int) {
	lrw.statusCode = code
	lrw.ResponseWriter.WriteHeader(code)
}

func initializeEnv() *nimbusEnv.Env {
	// Initialize logger
	logger := slog.New(&logging.ContextHandler{
		Handler: slog.NewTextHandler(
			os.Stderr,
			&slog.HandlerOptions{
				Level: slog.LevelDebug,
			})},
	)

	// Initialize registrar
	logger.Info("Initializing registrar")
	registrar := registrar.New()
	registrar.Set("ENVIRONMENT", os.Getenv("ENVIRONMENT"))
	registrar.Set("HOME", os.Getenv("HOME"))
	registrar.Set("DOMAIN", os.Getenv("DOMAIN"))
	registrar.Set("NIMBUS_STORAGE_CLASS", os.Getenv("NIMBUS_STORAGE_CLASS"))

	conn, err := pgx.Connect(
		context.Background(),
		fmt.Sprintf(
			"user=%s password=%s host=%s port=%s dbname=%s sslmode=disable",
			os.Getenv("DB_USER"),
			os.Getenv("DB_PASSWORD"),
			os.Getenv("DB_HOST"),
			os.Getenv("DB_PORT"),
			os.Getenv("DB_NAME"),
		))
	if err != nil {
		panic(err)
	}

	// Initialize the database connection
	logger.Info("Connecting to database")
	return nimbusEnv.NewEnvironment(logger, registrar, &database.Database{Queries: database.New(conn)})
}

func Start(port string, env *nimbusEnv.Env) {
	if env == nil {
		env = initializeEnv()
	}
	defer env.Database.Close()

	log.Printf("Serving at 0.0.0.0:%s...", port)
	router := mux.NewRouter()
	router.Use(injectEnvironment(env))
	router.Use(recoverMiddleware)
	addRoutes(router)

	http.Handle("/", router)
	http.ListenAndServe(":"+port, nil)
}

func recoverMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		environment, ok := r.Context().Value(envKey).(*nimbusEnv.Env)
		if !ok {
			environment = nimbusEnv.Null()
		}

		defer func() {
			if err := recover(); err != nil {
				environment.Logger.Error("Panic occurred: %v", err)
				http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			}
		}()

		w.Header().Set("Content-Type", "application/json")
		next.ServeHTTP(w, r)
	})
}

func injectEnvironment(env *nimbusEnv.Env) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if env == nil {
				env = nimbusEnv.Null()
			}
			r = r.WithContext(context.WithValue(r.Context(), envKey, env))
			next.ServeHTTP(w, r)
		})
	}
}

func logHTTPContext(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		environment, ok := r.Context().Value(envKey).(*nimbusEnv.Env)
		if !ok {
			environment = nimbusEnv.Null()
		}

		r = r.WithContext(logging.AppendCtx(r.Context(), slog.String("method", r.Method)))
		r = r.WithContext(logging.AppendCtx(r.Context(), slog.String("path", r.URL.RequestURI())))
		lrw := &logResponseWriter{w, http.StatusOK}
		environment.Logger.InfoContext(r.Context(), "Request received")
		next.ServeHTTP(lrw, r)
		environment.Logger.LogAttrs(
			r.Context(),
			slog.LevelInfo,
			"Request completed",
			slog.Duration("duration", time.Since(start)),
			slog.Int("status", lrw.statusCode),
		)
	})
}

func addRoutes(router *mux.Router) {
	router.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	}).Methods("GET")

	router.HandleFunc("/deploy", handlers.Deploy).Methods("POST")
}
