// Bridges only NGINX-specific behaviors: auth-signin and SPA error_page fallback.
package routehelper

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"
	"time"
)

type AuthRule struct {
	URL             string   `json:"url"`
	IdentityHeaders []string `json:"identity_headers"`
	SignIn          string   `json:"sign_in"`
}
type Config struct {
	ConnectTimeout        string              `json:"connect_timeout"`
	ResponseHeaderTimeout string              `json:"response_header_timeout"`
	Auth                  map[string]AuthRule `json:"auth"`
	SPA                   map[string]string   `json:"spa"`
}
type Bridge struct {
	config    Config
	client    *http.Client
	transport http.RoundTripper
}

func hostname(authority string) string {
	if h, _, err := net.SplitHostPort(authority); err == nil {
		return strings.ToLower(h)
	}
	return strings.ToLower(authority)
}
func (b *Bridge) auth(w http.ResponseWriter, r *http.Request) {
	rule, ok := b.config.Auth[hostname(r.Host)]
	if !ok {
		http.Error(w, "Unknown protected host", http.StatusForbidden)
		return
	}
	// Original path is carried by ext_authz (no pathOverride/prefix). Never trust
	// client-supplied X-Forwarded-Host or X-Forwarded-Uri for the return URL.
	if !strings.HasPrefix(r.URL.RequestURI(), "/") {
		http.Error(w, "Invalid path", 400)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	check, err := http.NewRequestWithContext(ctx, http.MethodGet, rule.URL, nil)
	if err != nil {
		http.Error(w, "Auth unavailable", 503)
		return
	}
	check.Header.Set("Cookie", r.Header.Get("Cookie"))
	check.Header.Set("Authorization", r.Header.Get("Authorization"))
	response, err := b.client.Do(check)
	if err != nil {
		http.Error(w, "Auth unavailable", 503)
		return
	}
	defer response.Body.Close()
	// NGINX accepts 2xx auth subresponses. Fail closed on unexpected replies.
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		for _, header := range rule.IdentityHeaders {
			value := response.Header.Get(header)
			if value == "" {
				http.Error(w, "Missing authenticated identity", 503)
				return
			}
			w.Header().Set(header, value)
		}
		w.WriteHeader(200)
		return
	}
	if response.StatusCode == 401 {
		if rule.SignIn == "" {
			http.Error(w, "Unauthorized", 401)
			return
		}
		destination, err := url.Parse(rule.SignIn)
		if err != nil {
			http.Error(w, "Invalid sign-in URL", 503)
			return
		}
		query := destination.Query()
		for key, values := range query {
			for i, value := range values {
				values[i] = strings.NewReplacer("$scheme", "https", "$host", hostname(r.Host), "$request_uri", r.URL.RequestURI()).Replace(value)
			}
			query[key] = values
		}
		destination.RawQuery = query.Encode()
		w.Header().Set("Location", destination.String())
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusFound)
		return
	}
	if response.StatusCode == 403 {
		http.Error(w, "Forbidden", 403)
		return
	}
	http.Error(w, "Auth unavailable", 503)
}
func (b *Bridge) spa(w http.ResponseWriter, r *http.Request) {
	backend, ok := b.config.SPA[hostname(r.Host)]
	if !ok {
		http.Error(w, "Unknown frontend host", 404)
		return
	}
	target, err := url.Parse(backend)
	if err != nil {
		http.Error(w, "Invalid frontend", 502)
		return
	}
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.Transport = b.transport
	proxy.FlushInterval = -1
	proxy.ErrorLog = log.New(io.Discard, "", 0) // Never log request cookies, query strings or auth headers.
	proxy.ModifyResponse = func(response *http.Response) error {
		if response.StatusCode != 404 {
			return nil
		}
		// Match NGINX error_page 404 =200 /index.html internal GET/HEAD redirect.
		response.Body.Close()
		fallbackURL := *target
		fallbackURL.Path = "/index.html"
		fallbackURL.RawPath = ""
		fallbackURL.RawQuery = ""
		method := http.MethodGet
		if r.Method == http.MethodHead {
			method = http.MethodHead
		}
		fallback, err := http.NewRequestWithContext(r.Context(), method, fallbackURL.String(), nil)
		if err != nil {
			return err
		}
		fallback.Host = r.Host
		// Forward representation selectors, but not stale range/conditional/body headers.
		for _, key := range []string{"Accept", "Accept-Encoding", "Accept-Language", "Cookie", "Authorization", "User-Agent"} {
			if value := r.Header.Get(key); value != "" {
				fallback.Header.Set(key, value)
			}
		}
		replacement, err := b.transport.RoundTrip(fallback)
		if err != nil {
			return err
		}
		if replacement.StatusCode != 200 {
			replacement.Body.Close()
			return fmt.Errorf("SPA index unavailable")
		}
		*response = *replacement
		return nil
	}
	proxy.ServeHTTP(w, r)
}

// Run serves a per-service compatibility adapter with no Kubernetes credentials.
func Run(ctx context.Context) error {
	data, err := os.ReadFile("/config/config.json")
	if err != nil {
		return fmt.Errorf("cannot read route helper configuration")
	}
	var cfg Config
	if json.Unmarshal(data, &cfg) != nil {
		return fmt.Errorf("invalid route helper configuration")
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	connect := 5 * time.Second
	response := 60 * time.Second
	if cfg.ConnectTimeout != "" {
		connect, err = time.ParseDuration(cfg.ConnectTimeout)
		if err != nil || connect <= 0 {
			return fmt.Errorf("invalid connect timeout")
		}
	}
	if cfg.ResponseHeaderTimeout != "" {
		response, err = time.ParseDuration(cfg.ResponseHeaderTimeout)
		if err != nil || response < 0 {
			return fmt.Errorf("invalid response timeout")
		}
	}
	transport.DialContext = (&net.Dialer{Timeout: connect, KeepAlive: 30 * time.Second}).DialContext
	transport.ResponseHeaderTimeout = response
	b := &Bridge{config: cfg, transport: transport, client: &http.Client{Transport: transport, Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
	health := http.NewServeMux()
	health.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	servers := []*http.Server{
		{Addr: ":9000", Handler: http.HandlerFunc(b.auth), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 65536},
		{Addr: ":9001", Handler: http.HandlerFunc(b.spa), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 65536},
		{Addr: ":9002", Handler: health, ReadHeaderTimeout: 5 * time.Second},
	}
	failures := make(chan error, len(servers))
	for _, server := range servers {
		go func(s *http.Server) { failures <- s.ListenAndServe() }(server)
	}
	select {
	case <-ctx.Done():
		err = nil
	case err = <-failures:
	}
	for _, server := range servers {
		_ = server.Close()
	}
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}
