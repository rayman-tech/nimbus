package kubernetes

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/util/validation"

	"nimbus/internal/models"
)

// Nimbus interprets these settings and emits Gateway API / Envoy policy specs.
// They are not annotations natively interpreted by the Envoy Gateway controller.
const envoyAnnotationPrefix = "envoy.nimbus.dev/"
const nginxAnnotationPrefix = "nginx.ingress.kubernetes.io/"

func routeSettings(s *models.Service) (routeOptions, error) {
	copyService := *s
	a := make(map[string]string, len(s.Annotations))
	for k, v := range s.Annotations {
		a[k] = v
	}
	copyService.Annotations = a
	supported := map[string]bool{"auth-provider": true, "authentik-service": true, "authentik-namespace": true, "authentik-port": true}
	alias := func(key, legacy, value string) error {
		if old, exists := a[legacy]; exists && old != value {
			return fmt.Errorf("conflicting %s%s and deprecated %s", envoyAnnotationPrefix, key, legacy)
		}
		a[legacy] = value
		return nil
	}
	for _, key := range []string{"ssl-redirect", "auth-url", "auth-signin", "auth-response-headers", "enable-cors", "cors-allow-origin", "cors-allow-credentials", "cors-allow-methods", "cors-allow-headers", "cors-expose-headers", "cors-max-age", "body-size", "cluster-issuer", "backend-protocol", "spa"} {
		supported[key] = true
		v, exists := a[envoyAnnotationPrefix+key]
		if !exists {
			continue
		}
		if v == "" {
			return routeOptions{}, fmt.Errorf("%s%s cannot be empty", envoyAnnotationPrefix, key)
		}
		legacy := nginxAnnotationPrefix + key
		switch key {
		case "body-size":
			legacy = nginxAnnotationPrefix + "proxy-body-size"
		case "cluster-issuer":
			legacy = "cert-manager.io/cluster-issuer"
		case "backend-protocol":
			switch v {
			case "h2c":
				v = "GRPC"
			case "http":
				v = "HTTP"
			default:
				return routeOptions{}, fmt.Errorf("Envoy backend-protocol must be http or h2c; backend TLS is not configured by this option")
			}
			if v == "HTTP" {
				for _, feature := range s.Features {
					if feature == "grpc" {
						return routeOptions{}, fmt.Errorf("backend-protocol=http conflicts with features: [grpc]")
					}
				}
			}
		case "spa":
			if v != "true" && v != "false" {
				return routeOptions{}, fmt.Errorf("Envoy spa must be true or false")
			}
			if v == "true" {
				copyService.Features = append(append([]string{}, s.Features...), "spa")
			} else {
				for _, feature := range s.Features {
					if feature == "spa" {
						return routeOptions{}, fmt.Errorf("spa=false conflicts with features: [spa]")
					}
				}
				if a[nginxAnnotationPrefix+"configuration-snippet"] != "" {
					return routeOptions{}, fmt.Errorf("spa=false conflicts with deprecated configuration-snippet")
				}
			}
			continue
		}
		if err := alias(key, legacy, v); err != nil {
			return routeOptions{}, err
		}
	}
	if a[nginxAnnotationPrefix+"backend-protocol"] == "HTTP" {
		for _, feature := range s.Features {
			if feature == "grpc" {
				return routeOptions{}, fmt.Errorf("HTTP backend conflicts with features: [grpc]")
			}
		}
	}
	o, err := legacyRouteSettings(&copyService)
	if err != nil {
		return o, err
	}
	if provider, exists := a[envoyAnnotationPrefix+"auth-provider"]; exists {
		if provider != "authentik" {
			return o, fmt.Errorf("auth-provider must be authentik")
		}
		if o.AuthURL != "" || o.SignIn != "" {
			return o, fmt.Errorf("auth-provider=authentik conflicts with auth-url/auth-signin")
		}
		if o.GRPC {
			return o, fmt.Errorf("auth-provider=authentik requires an HTTP route for browser callbacks")
		}
		o.Authentik = true
		o.AuthentikService = "authentik-server"
		o.AuthentikNamespace = "authentik"
		o.AuthentikPort = 80
		if value, ok := a[envoyAnnotationPrefix+"authentik-service"]; ok {
			o.AuthentikService = value
		}
		if value, ok := a[envoyAnnotationPrefix+"authentik-namespace"]; ok {
			o.AuthentikNamespace = value
		}
		if len(validation.IsDNS1035Label(o.AuthentikService)) != 0 || len(validation.IsDNS1123Label(o.AuthentikNamespace)) != 0 {
			return o, fmt.Errorf("invalid Authentik service or namespace")
		}
		if value, ok := a[envoyAnnotationPrefix+"authentik-port"]; ok {
			port, e := strconv.ParseInt(value, 10, 32)
			if e != nil || port < 1 || port > 65535 {
				return o, fmt.Errorf("authentik-port must be between 1 and 65535")
			}
			o.AuthentikPort = port
		}
		o.IdentityHeaders = append(o.IdentityHeaders, "X-Authentik-Username", "X-Authentik-Email", "X-Authentik-Name", "X-Authentik-Uid", "X-Authentik-Groups")
	} else {
		for _, key := range []string{"authentik-service", "authentik-namespace", "authentik-port"} {
			if _, exists := a[envoyAnnotationPrefix+key]; exists {
				return o, fmt.Errorf("%s requires auth-provider=authentik", key)
			}
		}
	}

	for _, setting := range []struct {
		key    string
		target *string
		legacy []string
		zero   bool
	}{
		{"connect-timeout", &o.Connect, []string{"proxy-connect-timeout"}, false},
		{"stream-idle-timeout", &o.Idle, []string{"proxy-read-timeout", "proxy-send-timeout"}, true},
		{"request-timeout", &o.Request, nil, true},
		{"max-stream-duration", &o.MaxStream, nil, true},
	} {
		supported[setting.key] = true
		v, exists := a[envoyAnnotationPrefix+setting.key]
		if !exists {
			continue
		}
		duration, e := envoyDuration(v, setting.zero)
		if e != nil {
			return o, fmt.Errorf("%s: %w", setting.key, e)
		}
		for _, oldKey := range setting.legacy {
			if old, ok := a[nginxAnnotationPrefix+oldKey]; ok {
				oldDuration, e := time.ParseDuration(old + "s")
				if e != nil || oldDuration != duration {
					return o, fmt.Errorf("%s conflicts with deprecated %s", setting.key, oldKey)
				}
			}
		}
		*setting.target = v
	}
	for _, setting := range []struct {
		key     string
		target  *string
		pattern string
	}{
		{"grpc-service", &o.GRPCService, `^[A-Za-z_][A-Za-z0-9_]*(\.[A-Za-z_][A-Za-z0-9_]*)*$`},
		{"grpc-method", &o.GRPCMethod, `^[A-Za-z_][A-Za-z0-9_]*$`},
	} {
		supported[setting.key] = true
		if v, exists := a[envoyAnnotationPrefix+setting.key]; exists {
			if !o.GRPC || len(v) > 1024 || !regexp.MustCompile(setting.pattern).MatchString(v) {
				return o, fmt.Errorf("%s requires a valid protobuf name and a gRPC route", setting.key)
			}
			*setting.target = v
		}
	}
	for _, key := range []string{"grpc-retry-count", "grpc-retry-on", "grpc-per-retry-timeout"} {
		supported[key] = true
	}
	count, retry := a[envoyAnnotationPrefix+"grpc-retry-count"]
	triggers, retryOn := a[envoyAnnotationPrefix+"grpc-retry-on"]
	perRetry, retryTimeout := a[envoyAnnotationPrefix+"grpc-per-retry-timeout"]
	if retry || retryOn || retryTimeout {
		n, e := strconv.Atoi(count)
		if !o.GRPC || !retry || !retryOn || e != nil || n < 1 || n > 10 {
			return o, fmt.Errorf("gRPC retries require grpc-retry-count (1–10), grpc-retry-on, and a gRPC route")
		}
		on := csv(triggers)
		allowed := map[string]bool{"cancelled": true, "deadline-exceeded": true, "internal": true, "resource-exhausted": true, "unavailable": true, "connect-failure": true, "refused-stream": true, "reset-before-request": true}
		if len(on) == 0 {
			return o, fmt.Errorf("grpc-retry-on cannot be empty")
		}
		for _, trigger := range on {
			if !allowed[trigger] {
				return o, fmt.Errorf("unsupported grpc-retry-on trigger %q", trigger)
			}
		}
		o.Retry = map[string]interface{}{"numRetries": int64(n), "retryOn": map[string]interface{}{"triggers": strs(on)}}
		if retryTimeout {
			if _, e := envoyDuration(perRetry, false); e != nil {
				return o, fmt.Errorf("grpc-per-retry-timeout: %w", e)
			}
			o.Retry["perRetry"] = map[string]interface{}{"timeout": perRetry}
		}
	}
	for key := range a {
		if strings.HasPrefix(key, envoyAnnotationPrefix) && !supported[strings.TrimPrefix(key, envoyAnnotationPrefix)] {
			return o, fmt.Errorf("unsupported Envoy annotation %q", key)
		}
	}
	return o, nil
}

func envoyDuration(value string, zero bool) (time.Duration, error) {
	// Match Gateway API Duration syntax, rather than accepting unsupported Go units.
	if !regexp.MustCompile(`^([0-9]{1,5}(h|m|s|ms)){1,4}$`).MatchString(value) {
		return 0, fmt.Errorf("use a duration such as 5s, 250ms, or 5m")
	}
	d, err := time.ParseDuration(value)
	if err != nil || d < 0 || (!zero && d == 0) || d > 24*time.Hour {
		return 0, fmt.Errorf("duration must be %s and at most 24h", map[bool]string{true: "nonnegative", false: "positive"}[zero])
	}
	return d, nil
}
