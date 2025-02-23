package api

import (
	"log"
	"net/http"

	"github.com/gorilla/mux"
)

func Start(port string) {
	log.Printf("Serving at 0.0.0.0:%s...", port)
	router := mux.NewRouter()
	router.Use(recoverMiddleware)

	http.Handle("/", router)
	http.ListenAndServe(":"+port, nil)
}

func recoverMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if err := recover(); err != nil {
				log.Printf("Panic occurred: %v", err)
				http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			}
		}()

		w.Header().Set("Content-Type", "application/json")
		next.ServeHTTP(w, r)
	})
}
