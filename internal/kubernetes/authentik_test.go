package kubernetes

import (
	"context"
	"github.com/goccy/go-yaml"
	"nimbus/internal/models"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	dynamicfake "k8s.io/client-go/dynamic/fake"
)

func TestAuthentikDirectAuthAndCallbackIsolation(t *testing.T) {
	s := publicService()
	if err := yaml.Unmarshal([]byte("auth:\n  provider: authentik\n"), s); err != nil {
		t.Fatal(err)
	}
	cfg := routeConfig()
	cfg.RouteHelperImage = ""
	p, err := GenerateRoutePlan("app", s, nil, "main", cfg)
	if err != nil {
		t.Fatal(err)
	}
	if p.HasHelper {
		t.Fatal("native auth must not require compatibility helper")
	}
	policy := resourceOf(t, p, securityPolicies)
	path, _, _ := unstructured.NestedString(policy.Object, "spec", "extAuth", "http", "path")
	failOpen, _, _ := unstructured.NestedBool(policy.Object, "spec", "extAuth", "failOpen")
	if path != "/outpost.goauthentik.io/auth/envoy" || failOpen {
		t.Fatalf("unexpected auth policy: %v", policy.Object)
	}
	refs, _, _ := unstructured.NestedSlice(policy.Object, "spec", "extAuth", "http", "backendRefs")
	if ref := refs[0].(map[string]interface{}); ref["namespace"] != "authentik" || ref["name"] != "authentik-server" || ref["port"] != int64(80) {
		t.Fatal("wrong outpost namespace")
	}
	targets, _, _ := unstructured.NestedSlice(policy.Object, "spec", "targetRefs")
	if len(targets) != 1 || targets[0].(map[string]interface{})["name"] != "web-route" {
		t.Fatal("auth must only target the application route")
	}
	var callback *unstructured.Unstructured
	for _, r := range p.Resources {
		if r.GVR == httpRoutes && r.Object.GetName() == "web-authentik" {
			callback = r.Object
		}
		if r.GVR == deployments {
			t.Fatal("unexpected helper deployment")
		}
	}
	if callback == nil {
		t.Fatal("missing outpost callback route")
	}
	rules, _, _ := unstructured.NestedSlice(callback.Object, "spec", "rules")
	matches := rules[0].(map[string]interface{})["matches"].([]interface{})
	if matches[0].(map[string]interface{})["path"].(map[string]interface{})["value"] != "/outpost.goauthentik.io" {
		t.Fatal("callback bypass too broad")
	}
	headers, _, _ := unstructured.NestedStringSlice(resourceOf(t, p, clientPolicies).Object, "spec", "headers", "earlyRequestHeaders", "remove")
	found := false
	for _, h := range headers {
		if h == "X-Authentik-Email" {
			found = true
		}
	}
	if !found {
		t.Fatal("forged identity header not stripped")
	}
}

func TestAuthentikConfigurationRejectsAmbiguity(t *testing.T) {
	cases := []map[string]string{
		{"envoy.nimbus.dev/auth-provider": "unknown"},
		{"envoy.nimbus.dev/authentik-service": "authentik-server"},
		{"envoy.nimbus.dev/auth-url": "https://old.example.com/auth"},
		{"nginx.ingress.kubernetes.io/auth-url": "https://old.example.com/auth"},
		{"envoy.nimbus.dev/auth-provider": "authentik", "envoy.nimbus.dev/authentik-port": "0"},
		{"envoy.nimbus.dev/auth-provider": "authentik", "envoy.nimbus.dev/authentik-namespace": "../other"},
		{"envoy.nimbus.dev/backend-protocol": "h2c"},
	}
	for _, a := range cases {
		s := publicService()
		s.Auth = &models.Auth{Provider: "authentik"}
		s.Annotations = a
		if _, err := routeSettings(s); err == nil {
			t.Fatalf("accepted invalid config %v", a)
		}
	}
}

func TestStructuredAuthValidation(t *testing.T) {
	for _, input := range []string{
		"auth: {}",
		"auth: {provider: unknown}",
		"auth: {provider: authentik, service: other}",
		"auth: {provider: authentik, namespace: other}",
		"auth: {provider: authentik, port: 8080}",
		"auth: {provider: authentik, authentik: {service: other}}",
		"auth: {provder: authentik}",
	} {
		s := publicService()
		err := yaml.Unmarshal([]byte(input), s)
		if err == nil {
			err = ValidateRouting(s)
		}
		if err == nil {
			t.Fatalf("accepted invalid auth: %s", input)
		}
	}
	for _, kind := range []string{"private", "tcp", "grpc"} {
		s := publicService()
		s.Auth = &models.Auth{Provider: "authentik"}
		switch kind {
		case "private":
			s.Public = false
		case "tcp":
			s.Template = "tcp"
		case "grpc":
			s.Features = []string{"grpc"}
		}
		if err := ValidateRouting(s); err == nil {
			t.Fatalf("accepted auth for %s", kind)
		}
	}
}

func TestAuthentikCallbackPrunedWhenDisabled(t *testing.T) {
	previous := dynamicClient
	defer func() { dynamicClient = previous }()
	cb := &unstructured.Unstructured{Object: map[string]interface{}{"apiVersion": "gateway.networking.k8s.io/v1", "kind": "HTTPRoute", "metadata": ownedMetadata("app", "web-authentik", "app", "web")}}
	dynamicClient = dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), cb)
	p, err := GenerateRoutePlan("app", publicService(), nil, "main", routeConfig())
	if err != nil {
		t.Fatal(err)
	}
	if err = pruneRouteResources(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	if _, err = dynamicClient.Resource(httpRoutes).Namespace("app").Get(context.Background(), "web-authentik", metav1.GetOptions{}); err == nil {
		t.Fatal("obsolete callback retained")
	}
}
