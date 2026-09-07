package kubernetes

import (
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"nimbus/internal/config"
	"nimbus/internal/models"
)

type routeOptions struct {
	GRPC, SPA               bool
	Authentik               bool
	AuthURL, SignIn         string
	IdentityHeaders         []string
	Issuer                  string
	BodyLimit               int64
	Connect, Idle           string
	Request, MaxStream      string
	GRPCService, GRPCMethod string
	Retry                   map[string]interface{}
	CORS                    map[string]interface{}
}

func legacyRouteSettings(s *models.Service) (routeOptions, error) {
	o := routeOptions{Issuer: "letsencrypt-prod", BodyLimit: 1048576, Connect: "5s", Idle: "60s", Request: "0s", MaxStream: "0s"}
	for _, f := range s.Features {
		switch f {
		case "grpc":
			o.GRPC = true
		case "spa":
			o.SPA = true
		}
	}
	a := s.Annotations
	const prefix = "nginx.ingress.kubernetes.io/"
	supported := map[string]bool{}
	for _, k := range []string{"ssl-redirect", "auth-url", "auth-signin", "auth-response-headers", "enable-cors", "cors-allow-origin", "cors-allow-credentials", "cors-allow-methods", "cors-allow-headers", "cors-expose-headers", "cors-max-age", "backend-protocol", "configuration-snippet", "proxy-body-size", "proxy-connect-timeout", "proxy-read-timeout", "proxy-send-timeout"} {
		supported[prefix+k] = true
	}
	for k := range a {
		if strings.HasPrefix(k, "cert-manager.io/") && k != "cert-manager.io/cluster-issuer" {
			return o, fmt.Errorf("unsupported certificate annotation %q; configure the Certificate explicitly", k)
		}
		if strings.HasPrefix(k, prefix) && !supported[k] {
			return o, fmt.Errorf("unsupported NGINX annotation %q; cannot safely translate it to Envoy", k)
		}
	}
	if v := a[prefix+"ssl-redirect"]; v != "" && v != "true" {
		return o, fmt.Errorf("public Envoy routes require ssl-redirect=true")
	}
	if v := a["cert-manager.io/cluster-issuer"]; v != "" {
		o.Issuer = v
	}
	if v := a[prefix+"backend-protocol"]; v != "" {
		switch v {
		case "GRPC":
			o.GRPC = true
		case "HTTP":
			o.GRPC = false
		default:
			return o, fmt.Errorf("unsupported backend-protocol %q", v)
		}
	}
	if v := a[prefix+"configuration-snippet"]; v != "" {
		if strings.Join(strings.Fields(v), " ") != "proxy_intercept_errors on; error_page 404 =200 /index.html;" {
			return o, fmt.Errorf("arbitrary NGINX configuration-snippet cannot be translated")
		}
		o.SPA = true
	}
	if o.GRPC && o.SPA {
		return o, fmt.Errorf("grpc and spa features cannot be combined")
	}
	o.AuthURL = a[prefix+"auth-url"]
	o.SignIn = a[prefix+"auth-signin"]
	for _, v := range []string{o.AuthURL, o.SignIn} {
		if v != "" {
			u, e := url.Parse(v)
			if e != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.Fragment != "" {
				return o, fmt.Errorf("auth URLs must be absolute HTTP(S) URLs without embedded credentials or fragments")
			}
		}
	}
	if o.SignIn != "" && o.AuthURL == "" {
		return o, fmt.Errorf("auth-signin requires auth-url")
	}
	if o.SignIn != "" {
		expanded := strings.NewReplacer("$scheme", "https", "$host", "example.com", "$request_uri", "/").Replace(o.SignIn)
		if strings.Contains(expanded, "$") {
			return o, fmt.Errorf("unsupported variable in auth-signin")
		}
	}
	o.IdentityHeaders = csv(a[prefix+"auth-response-headers"])
	for _, h := range o.IdentityHeaders {
		if !regexp.MustCompile(`^[A-Za-z0-9-]+$`).MatchString(h) || strings.EqualFold(h, "Set-Cookie") || strings.EqualFold(h, "Authorization") {
			return o, fmt.Errorf("unsupported auth response header %q", h)
		}
	}
	if v := a[prefix+"proxy-body-size"]; v != "" {
		m := regexp.MustCompile(`^([0-9]+)([kKmMgG]?)$`).FindStringSubmatch(v)
		if m == nil {
			return o, fmt.Errorf("invalid proxy-body-size")
		}
		n, e := strconv.ParseInt(m[1], 10, 64)
		if e != nil {
			return o, e
		}
		factor := int64(1)
		switch strings.ToLower(m[2]) {
		case "k":
			factor = 1024
		case "m":
			factor = 1024 * 1024
		case "g":
			factor = 1024 * 1024 * 1024
		}
		if n > 1<<40/factor {
			return o, fmt.Errorf("proxy-body-size exceeds supported range")
		}
		o.BodyLimit = n * factor
	}
	for _, k := range []string{"proxy-connect-timeout", "proxy-read-timeout", "proxy-send-timeout"} {
		if v := a[prefix+k]; v != "" {
			n, e := strconv.Atoi(v)
			if e != nil || n <= 0 || n > 86400 {
				return o, fmt.Errorf("%s must be 1–86400 seconds", k)
			}
			if k == "proxy-connect-timeout" {
				o.Connect = v + "s"
			} else {
				o.Idle = v + "s"
			}
		}
	}
	if v := a[prefix+"enable-cors"]; v != "" && v != "false" && v != "true" {
		return o, fmt.Errorf("enable-cors must be true or false")
	}
	if a[prefix+"enable-cors"] == "true" {
		origins := csv(a[prefix+"cors-allow-origin"])
		if len(origins) == 0 {
			origins = []string{"*"}
		}
		methods := csv(a[prefix+"cors-allow-methods"])
		if len(methods) == 0 {
			methods = []string{"GET", "PUT", "POST", "DELETE", "PATCH", "OPTIONS"}
		}
		headers := csv(a[prefix+"cors-allow-headers"])
		if len(headers) == 0 {
			headers = []string{"DNT", "Keep-Alive", "User-Agent", "X-Requested-With", "If-Modified-Since", "Cache-Control", "Content-Type", "Range", "Authorization"}
		}
		credentials := true
		if v := a[prefix+"cors-allow-credentials"]; v != "" {
			b, e := strconv.ParseBool(v)
			if e != nil {
				return o, e
			}
			credentials = b
		}
		age := 1728000
		if v := a[prefix+"cors-max-age"]; v != "" {
			n, e := strconv.Atoi(v)
			if e != nil || n < 0 || n > 31536000 {
				return o, fmt.Errorf("invalid cors-max-age")
			}
			age = n
		}
		o.CORS = map[string]interface{}{"allowOrigins": strs(origins), "allowMethods": strs(methods), "allowHeaders": strs(headers), "allowCredentials": credentials, "maxAge": (time.Duration(age) * time.Second).String()}
		if h := csv(a[prefix+"cors-expose-headers"]); len(h) > 0 {
			o.CORS["exposeHeaders"] = strs(h)
		}
	}
	return o, nil
}
func csv(v string) []string {
	var out []string
	for _, s := range strings.Split(v, ",") {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}
func strs(v []string) []interface{} {
	o := make([]interface{}, len(v))
	for i, s := range v {
		o[i] = s
	}
	return o
}
func ValidateRouting(s *models.Service) error {
	if s.Template != "http" || !s.Public {
		if s.Auth != nil {
			return fmt.Errorf("auth requires a public HTTP service")
		}
		return nil
	}
	_, e := routeSettings(s)
	return e
}

// ValidateRoutePrerequisites runs before any service networking is reconciled.
func ValidateRoutePrerequisites(s *models.Service, cfg *config.Config) error {
	if s.Template != "http" || !s.Public {
		if s.Auth != nil {
			return fmt.Errorf("auth requires a public HTTP service")
		}
		return nil
	}
	o, e := routeSettings(s)
	if e != nil {
		return e
	}
	if (o.SPA || o.AuthURL != "") && (cfg == nil || !regexp.MustCompile(`^[A-Za-z0-9._:/-]+@sha256:[a-f0-9]{64}$`).MatchString(cfg.RouteHelperImage)) {
		return fmt.Errorf("NIMBUS_ROUTE_HELPER_IMAGE must be a digest-pinned Nimbus image supporting route-helper for auth/spa services")
	}
	return nil
}
