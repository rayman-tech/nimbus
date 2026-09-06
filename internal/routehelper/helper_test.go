package routehelper

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestAuth(t *testing.T) {
	for _, status := range []int{200, 202, 401, 403, 500, 302} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/sessions/whoami" || r.Method != "GET" || r.Header.Get("Cookie") != "session=real" {
					t.Error("wrong session check")
				}
				w.Header().Set("X-Auth-Request-User", "real-user")
				w.Header().Set("X-Auth-Request-Email", "real@example.com")
				w.WriteHeader(status)
			}))
			defer upstream.Close()
			b := Bridge{config: Config{Auth: map[string]AuthRule{"metrics.example.com": {URL: upstream.URL + "/sessions/whoami", IdentityHeaders: []string{"X-Auth-Request-User", "X-Auth-Request-Email"}, SignIn: "https://login.example.com/oauth2/start?rd=$scheme://$host$request_uri"}}}, client: upstream.Client()}
			r := httptest.NewRequest("POST", "https://metrics.example.com/a?x=1&y=2", nil)
			r.Header.Set("Cookie", "session=real")
			r.Header.Set("X-Auth-Request-User", "forged")
			r.Header.Set("X-Forwarded-Host", "evil.example")
			w := httptest.NewRecorder()
			b.auth(w, r)
			expected := status
			if status == 202 {
				expected = 200
			}
			if status == 401 {
				expected = 302
			}
			if status == 500 || status == 302 {
				expected = 503
			}
			if w.Code != expected {
				t.Fatalf("got %d want %d", w.Code, expected)
			}
			if status == 401 {
				u, _ := url.Parse(w.Header().Get("Location"))
				if u.Query().Get("rd") != "https://metrics.example.com/a?x=1&y=2" {
					t.Fatal("unsafe or truncated redirect")
				}
			}
			if expected == 200 && w.Header().Get("X-Auth-Request-User") != "real-user" {
				t.Fatal("identity not authoritative")
			}
		})
	}
}
func TestAuthFailsClosed(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(202) }))
	b := Bridge{config: Config{Auth: map[string]AuthRule{"metrics.example.com": {URL: up.URL, IdentityHeaders: []string{"X-Auth-Request-User", "X-Auth-Request-Email"}}}}, client: up.Client()}
	for _, host := range []string{"metrics.example.com", "evil.example"} {
		w := httptest.NewRecorder()
		b.auth(w, httptest.NewRequest("GET", "https://"+host+"/", nil))
		if w.Code < 400 {
			t.Fatal("missing identity/unknown host allowed")
		}
	}
	up.Close()
	w := httptest.NewRecorder()
	b.auth(w, httptest.NewRequest("GET", "https://metrics.example.com/", nil))
	if w.Code != 503 {
		t.Fatal("unavailable auth allowed")
	}
}
func TestSPAFallback(t *testing.T) {
	for _, method := range []string{"GET", "HEAD", "POST"} {
		t.Run(method, func(t *testing.T) {
			calls := 0
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.URL.Path == "/index.html" {
					want := "GET"
					if method == "HEAD" {
						want = "HEAD"
					}
					if r.Method != want {
						t.Error("wrong internal redirect method")
					}
					w.Header().Set("Content-Type", "text/html")
					io.WriteString(w, "<html>app</html>")
					return
				}
				http.NotFound(w, r)
			}))
			defer up.Close()
			b := Bridge{config: Config{SPA: map[string]string{"spa.example.com": up.URL}}, transport: up.Client().Transport}
			w := httptest.NewRecorder()
			b.spa(w, httptest.NewRequest(method, "https://spa.example.com/nested/path?x=1", nil))
			if w.Code != 200 || calls != 2 {
				t.Fatalf("fallback failed %d calls %d", w.Code, calls)
			}
		})
	}
}
func TestSPAStreamingAndNoOpenProxy(t *testing.T) {
	payload := strings.Repeat("data", 1024*1024)
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, payload) }))
	defer up.Close()
	b := Bridge{config: Config{SPA: map[string]string{"spa.example.com": up.URL}}, transport: up.Client().Transport}
	w := httptest.NewRecorder()
	b.spa(w, httptest.NewRequest("GET", "https://spa.example.com/asset.js", nil))
	if w.Body.String() != payload {
		t.Fatal("body corrupted")
	}
	w = httptest.NewRecorder()
	b.spa(w, httptest.NewRequest("GET", "https://evil.example/", nil))
	if w.Code != 404 {
		t.Fatal("open proxy")
	}
}
