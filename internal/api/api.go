package api

import (
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"nimbus/internal/api/handlers"
	"nimbus/internal/env"
	"nimbus/internal/logging"

	"github.com/gorilla/mux"
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

func Start(port string, env *env.Env) error {
	env.Logger.Info(fmt.Sprintf("Serving at 0.0.0.0:%s...", port))
	router := mux.NewRouter()
	router.Use(injectEnvironment(env))
	router.Use(recoverMiddleware)
	router.Use(logRequest)
	addRoutes(router)

	http.Handle("/", router)
	return http.ListenAndServe(":"+port, nil)
}

func recoverMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		e := env.FromContext(r.Context())

		defer func() {
			if err := recover(); err != nil {
				e.Logger.ErrorContext(r.Context(), "Panic occurred", slog.Any("panic", err))
				http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			}
		}()

		w.Header().Set("Content-Type", "application/json")
		next.ServeHTTP(w, r)
	})
}

func injectEnvironment(e *env.Env) func(http.Handler) http.Handler {
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

func logRequest(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		e := env.FromContext(r.Context())

		ctx := r.Context()
		logId := ulid.MustNew(ulid.Timestamp(start), ulid.DefaultEntropy())
		r = r.WithContext(logging.AppendCtx(ctx, slog.String("log_id", logId.String())))
		r = r.WithContext(logging.AppendCtx(r.Context(), slog.String("method", r.Method)))
		r = r.WithContext(logging.AppendCtx(r.Context(), slog.String("path", r.URL.RequestURI())))
		lrw := &logResponseWriter{w, http.StatusOK}
		e.Logger.InfoContext(r.Context(), "Request received")
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

func addRoutes(router *mux.Router) {
	router.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OK"))
	}).Methods("GET")

	router.HandleFunc("/deploy", handlers.Deploy).Methods("POST")
	router.HandleFunc("/projects", handlers.CreateProject).Methods("POST")
	router.HandleFunc("/projects", handlers.GetProjects).Methods("GET")
	router.HandleFunc("/projects/{name}/secrets", handlers.GetProjectSecrets).Methods("GET")
	router.HandleFunc("/projects/{name}/secrets", handlers.UpdateProjectSecrets).Methods("PUT")
	router.HandleFunc("/services", handlers.GetServices).Methods("GET")
	router.HandleFunc("/services/{name}", handlers.GetService).Methods("GET")
	router.HandleFunc("/services/{name}/logs", handlers.StreamLogs).Methods("GET")
	router.HandleFunc("/projects/{name}", handlers.DeleteProject).Methods("DELETE")
	router.HandleFunc("/branch", handlers.DeleteBranch).Methods("DELETE")
}
