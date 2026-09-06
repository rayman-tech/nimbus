package kubernetes

import (
	"reflect"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestEnvoyAnnotationAliases(t *testing.T) {
	for _, pair := range []struct{ key, old, value string }{
		{"auth-url", "auth-url", "https://idp.example.com/check"},
		{"enable-cors", "enable-cors", "true"},
		{"body-size", "proxy-body-size", "8m"},
	} {
		s := publicService()
		s.Annotations = map[string]string{nginxAnnotationPrefix + pair.old: pair.value}
		legacy, err := routeSettings(s)
		if err != nil {
			t.Fatal(err)
		}
		s.Annotations = map[string]string{envoyAnnotationPrefix + pair.key: pair.value}
		explicit, err := routeSettings(s)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(legacy, explicit) {
			t.Fatalf("%s changed behavior: %#v %#v", pair.key, legacy, explicit)
		}
	}
	s := publicService()
	s.Annotations = map[string]string{envoyAnnotationPrefix + "stream-idle-timeout": "1m", nginxAnnotationPrefix + "proxy-read-timeout": "60"}
	if _, err := routeSettings(s); err != nil {
		t.Fatal("equivalent aliases rejected", err)
	}
}

func TestEnvoyAnnotationValidation(t *testing.T) {
	for _, annotations := range []map[string]string{
		{envoyAnnotationPrefix + "auth-url": "https://new.example.com", nginxAnnotationPrefix + "auth-url": "https://old.example.com"},
		{envoyAnnotationPrefix + "connect-timeout": "0s"},
		{envoyAnnotationPrefix + "request-timeout": "-1s"},
		{envoyAnnotationPrefix + "request-timeout": "10us"},
		{envoyAnnotationPrefix + "stream-idle-timeout": "5m", nginxAnnotationPrefix + "proxy-send-timeout": "60"},
		{envoyAnnotationPrefix + "unknown": "true"},
		{envoyAnnotationPrefix + "backend-protocol": "https"},
		{envoyAnnotationPrefix + "backend-protocol": "http"},
		{envoyAnnotationPrefix + "grpc-method": "bad/method"},
		{envoyAnnotationPrefix + "grpc-retry-count": "2"},
		{envoyAnnotationPrefix + "grpc-retry-count": "2", envoyAnnotationPrefix + "grpc-retry-on": "anything"},
	} {
		s := publicService()
		s.Features = []string{"grpc"}
		s.Annotations = annotations
		if _, err := routeSettings(s); err == nil {
			t.Fatalf("accepted invalid annotations: %v", annotations)
		}
	}
	s := publicService()
	s.Annotations = map[string]string{envoyAnnotationPrefix + "grpc-service": "example.API"}
	if _, err := routeSettings(s); err == nil {
		t.Fatal("gRPC options accepted on HTTP route")
	}
}

func TestExplicitEnvoyGRPCPolicy(t *testing.T) {
	s := publicService()
	s.Annotations = map[string]string{
		envoyAnnotationPrefix + "backend-protocol":       "h2c",
		envoyAnnotationPrefix + "connect-timeout":        "250ms",
		envoyAnnotationPrefix + "stream-idle-timeout":    "0s",
		envoyAnnotationPrefix + "request-timeout":        "30s",
		envoyAnnotationPrefix + "max-stream-duration":    "10m",
		envoyAnnotationPrefix + "grpc-service":           "example.v1.API",
		envoyAnnotationPrefix + "grpc-method":            "Read",
		envoyAnnotationPrefix + "grpc-retry-count":       "2",
		envoyAnnotationPrefix + "grpc-retry-on":          "unavailable,connect-failure",
		envoyAnnotationPrefix + "grpc-per-retry-timeout": "5s",
	}
	p, err := GenerateRoutePlan("app", s, nil, "main", routeConfig())
	if err != nil {
		t.Fatal(err)
	}
	route := resourceOf(t, p, grpcRoutes)
	rules, _, _ := unstructured.NestedSlice(route.Object, "spec", "rules")
	match := rules[0].(map[string]interface{})["matches"].([]interface{})[0].(map[string]interface{})["method"].(map[string]interface{})
	if match["service"] != "example.v1.API" || match["method"] != "Read" {
		t.Fatal(match)
	}
	policy := resourceOf(t, p, backendPolicies)
	for key, want := range map[string]string{"requestTimeout": "30s", "streamIdleTimeout": "0s", "maxStreamDuration": "10m"} {
		got, _, _ := unstructured.NestedString(policy.Object, "spec", "timeout", "http", key)
		if got != want {
			t.Fatalf("%s: %s", key, got)
		}
	}
	count, _, _ := unstructured.NestedInt64(policy.Object, "spec", "retry", "numRetries")
	if count != 2 {
		t.Fatal(count)
	}
	service, err := GenerateServiceSpec("app", s, nil)
	if err != nil {
		t.Fatal(err)
	}
	if service.Spec.Ports[0].AppProtocol == nil || *service.Spec.Ports[0].AppProtocol != "kubernetes.io/h2c" {
		t.Fatal("backend is not h2c")
	}
}
