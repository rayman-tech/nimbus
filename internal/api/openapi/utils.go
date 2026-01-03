package openapi

import "net/http"

func getApiKey(r *http.Request) string {
	return r.Header.Get("X-API-Key")
}
