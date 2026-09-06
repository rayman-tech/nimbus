package kubernetes

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"nimbus/internal/config"
	"nimbus/internal/models"

	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	kubefake "k8s.io/client-go/kubernetes/fake"
)

func routeConfig() *config.Config {
	return &config.Config{Domain: "example.com", RouteHelperImage: "registry.example.com/nimbus@sha256:" + strings.Repeat("a", 64), RouteReadyTimeout: 1}
}
func publicService() *models.Service {
	return &models.Service{Name: "web", Template: "http", Public: true, Network: models.Network{Ports: []int32{8080}}}
}
func resourceOf(t *testing.T, p *RoutePlan, g schema.GroupVersionResource) *unstructured.Unstructured {
	t.Helper()
	for _, r := range p.Resources {
		if r.GVR == g {
			return r.Object
		}
	}
	t.Fatalf("missing %v", g)
	return nil
}
func TestRouteHostnameCompatibility(t *testing.T) {
	s := publicService()
	s.Ingress = "custom.example.com"
	existing := "preview.example.com"
	for _, tc := range []struct {
		branch, want string
		old          *string
	}{{"main", s.Ingress, &existing}, {"master", s.Ingress, nil}, {"feature", existing, &existing}} {
		p, e := GenerateRoutePlan("app", s, tc.old, tc.branch, routeConfig())
		if e != nil || p.Host != tc.want {
			t.Fatalf("%s: %v %v", tc.branch, p, e)
		}
	}
	s.Ingress = ""
	p, e := GenerateRoutePlan("app", s, nil, "main", routeConfig())
	if e != nil || !strings.HasSuffix(p.Host, ".example.com") {
		t.Fatal(p, e)
	}
	s.Public = false
	p, e = GenerateRoutePlan("app", s, nil, "main", routeConfig())
	if p != nil || e != nil {
		t.Fatal("private service got public route")
	}
}
func TestAuthCORSAndSPAStayTogether(t *testing.T) {
	s := publicService()
	s.Features = []string{"spa"}
	s.Annotations = map[string]string{"nginx.ingress.kubernetes.io/auth-url": "https://idp.example.com/sessions/whoami", "nginx.ingress.kubernetes.io/auth-signin": "https://login.example.com/start?rd=$scheme://$host$request_uri", "nginx.ingress.kubernetes.io/enable-cors": "true", "nginx.ingress.kubernetes.io/cors-allow-origin": "https://app.example.com"}
	p, e := GenerateRoutePlan("app", s, nil, "main", routeConfig())
	if e != nil {
		t.Fatal(e)
	}
	policy := resourceOf(t, p, securityPolicies)
	if v, _, _ := unstructured.NestedBool(policy.Object, "spec", "extAuth", "failOpen"); v {
		t.Fatal("auth fails open")
	}
	if _, ok, _ := unstructured.NestedMap(policy.Object, "spec", "cors"); !ok {
		t.Fatal("CORS lost when auth configured")
	}
	route := resourceOf(t, p, httpRoutes)
	rules, _, _ := unstructured.NestedSlice(route.Object, "spec", "rules")
	ref := rules[0].(object)["backendRefs"].([]interface{})[0].(object)
	if ref["name"] != "web-route-helper" {
		t.Fatal("SPA does not use helper")
	}
	cm := resourceOf(t, p, configMaps)
	data, _, _ := unstructured.NestedString(cm.Object, "data", "config.json")
	var cfg object
	if json.Unmarshal([]byte(data), &cfg) != nil || cfg["auth"] == nil || cfg["spa"] == nil {
		t.Fatal("missing helper config")
	}
	s.Annotations["nginx.ingress.kubernetes.io/auth-tls-secret"] = "unknown"
	if ValidateRouting(s) == nil {
		t.Fatal("unsupported auth annotation silently dropped")
	}
}
func TestGRPCUsesH2CAndNoHTTPBodyBuffer(t *testing.T) {
	s := publicService()
	s.Features = []string{"grpc"}
	p, e := GenerateRoutePlan("app", s, nil, "main", routeConfig())
	if e != nil {
		t.Fatal(e)
	}
	resourceOf(t, p, grpcRoutes)
	for _, r := range p.Resources {
		if r.GVR == extensionPolicies {
			t.Fatal("gRPC request buffered with Lua")
		}
	}
	svc, e := GenerateServiceSpec("app", s, nil)
	if e != nil || svc.Spec.Ports[0].AppProtocol == nil || *svc.Spec.Ports[0].AppProtocol != "kubernetes.io/h2c" {
		t.Fatal(svc, e)
	}
}
func fakeGateway(p *RoutePlan) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: object{"apiVersion": "gateway.networking.k8s.io/v1", "kind": "Gateway", "metadata": object{"name": p.GatewayName, "namespace": p.GatewayNamespace}, "spec": object{"gatewayClassName": "edge", "listeners": []interface{}{object{"name": "http", "port": int64(80), "protocol": "HTTP"}, object{"name": "other-app", "hostname": "other.example.com", "port": int64(443), "protocol": "HTTPS"}}}}}
}
func TestListenerMergeAndCleanupPreserveOtherApplications(t *testing.T) {
	p, _ := GenerateRoutePlan("app", publicService(), nil, "main", routeConfig())
	dc := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme())
	_ = dc.Tracker().Create(gateways, fakeGateway(p), p.GatewayNamespace)
	dynamicClient = dc
	client = kubefake.NewSimpleClientset()
	t.Cleanup(func() { dynamicClient = nil; client = nil })
	ctx := context.Background()
	if e := reconcileListener(ctx, p, false); e != nil {
		t.Fatal(e)
	}
	if e := reconcileListener(ctx, p, false); e != nil {
		t.Fatal(e)
	}
	if e := DeletePublicRoute(ctx, p.Namespace, p.ServiceName, routeConfig()); e != nil {
		t.Fatal(e)
	}
	g, _ := dynamicClient.Resource(gateways).Namespace(p.GatewayNamespace).Get(ctx, p.GatewayName, metav1.GetOptions{})
	ls, _, _ := unstructured.NestedSlice(g.Object, "spec", "listeners")
	if len(ls) != 2 || ls[1].(object)["name"] != "other-app" {
		t.Fatal("unrelated listeners changed", ls)
	}
}
func readyResource(r routeResource, p *RoutePlan) *unstructured.Unstructured {
	u := r.Object.DeepCopy()
	u.SetGeneration(1)
	conditions := []interface{}{object{"type": "Accepted", "status": "True", "observedGeneration": int64(1)}, object{"type": "ResolvedRefs", "status": "True", "observedGeneration": int64(1)}}
	u.Object["status"] = object{"ancestors": []interface{}{object{"ancestorRef": object{"name": p.GatewayName, "namespace": p.GatewayNamespace}, "conditions": conditions}}, "parents": []interface{}{object{"parentRef": object{"name": p.GatewayName, "namespace": p.GatewayNamespace}, "conditions": conditions}}, "conditions": []interface{}{object{"type": "Ready", "status": "True", "observedGeneration": int64(1)}}, "availableReplicas": int64(2), "observedGeneration": int64(1)}
	return u
}
func TestLegacyIngressOnlyRemovedAfterReady(t *testing.T) {
	for _, ready := range []bool{false, true} {
		t.Run(map[bool]string{false: "rejected-policy", true: "accepted"}[ready], func(t *testing.T) {
			s := publicService()
			s.Annotations = map[string]string{"nginx.ingress.kubernetes.io/auth-url": "http://idp.auth.svc/sessions/whoami"}
			host := "web.example.com"
			p, _ := GenerateRoutePlan("app", s, &host, "main", routeConfig())
			objs := []runtime.Object{}
			for _, r := range p.Resources {
				u := readyResource(r, p)
				if r.GVR == securityPolicies && !ready {
					delete(u.Object, "status")
				}
				if r.GVR == certificates {
					u.SetOwnerReferences([]metav1.OwnerReference{{Kind: "Ingress", APIVersion: "networking.k8s.io/v1", Name: "web-ingress", UID: types.UID("legacy")}})
					_ = unstructured.SetNestedField(u.Object, "Never", "spec", "privateKey", "rotationPolicy")
				}
				objs = append(objs, u)
			}
			dc := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), objs...)
			_ = dc.Tracker().Create(gateways, fakeGateway(p), p.GatewayNamespace)
			dynamicClient = dc
			client = kubefake.NewSimpleClientset(&networkingv1.Ingress{ObjectMeta: metav1.ObjectMeta{Name: "web-ingress", Namespace: "app", UID: "legacy"}})
			t.Cleanup(func() { dynamicClient = nil; client = nil })
			ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
			defer cancel()
			_, e := ReconcilePublicRoute(ctx, "app", s, &host, "main", routeConfig())
			_, legacyErr := client.NetworkingV1().Ingresses("app").Get(context.Background(), "web-ingress", metav1.GetOptions{})
			if ready {
				if e != nil || legacyErr == nil {
					t.Fatal("ready migration did not remove legacy", e, legacyErr)
				}
			} else {
				if e == nil || legacyErr != nil {
					t.Fatal("unready migration removed legacy", e, legacyErr)
				}
			}
			cert, _ := dynamicClient.Resource(certificates).Namespace("app").Get(context.Background(), "web-tls", metav1.GetOptions{})
			if len(cert.GetOwnerReferences()) != 0 {
				t.Fatal("legacy owner not detached")
			}
			if v, _, _ := unstructured.NestedString(cert.Object, "spec", "privateKey", "rotationPolicy"); v != "Never" {
				t.Fatal("certificate key policy changed")
			}
		})
	}
}
func TestCleanupRefusesUnmanagedResource(t *testing.T) {
	p, _ := GenerateRoutePlan("app", publicService(), nil, "main", routeConfig())
	u := resourceOf(t, p, httpRoutes).DeepCopy()
	u.SetLabels(nil)
	dynamicClient = dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), u)
	t.Cleanup(func() { dynamicClient = nil })
	if e := deleteOwned(context.Background(), httpRoutes, "app", u.GetName(), "app", "web"); e == nil {
		t.Fatal("unmanaged route deleted")
	}
}
func TestExportRouteFixtures(t *testing.T) {
	path := os.Getenv("NIMBUS_ROUTE_FIXTURES")
	if path == "" {
		t.Skip("optional offline Envoy translation fixture")
	}
	var objs []object
	for _, variant := range []string{"http", "grpc", "spa", "auth", "cors", "auth-cors"} {
		s := publicService()
		s.Name = variant
		s.Ingress = variant + ".example.com"
		s.Annotations = map[string]string{}
		if variant == "grpc" || variant == "spa" {
			s.Features = []string{variant}
		}
		if variant == "grpc" {
			s.Annotations = map[string]string{
				envoyAnnotationPrefix + "backend-protocol":       "h2c",
				envoyAnnotationPrefix + "stream-idle-timeout":    "300s",
				envoyAnnotationPrefix + "request-timeout":        "0s",
				envoyAnnotationPrefix + "grpc-service":           "example.v1.API",
				envoyAnnotationPrefix + "grpc-method":            "Read",
				envoyAnnotationPrefix + "grpc-retry-count":       "2",
				envoyAnnotationPrefix + "grpc-retry-on":          "unavailable",
				envoyAnnotationPrefix + "grpc-per-retry-timeout": "5s",
			}
		}
		if strings.Contains(variant, "auth") {
			s.Annotations[envoyAnnotationPrefix+"auth-url"] = "http://identity.auth.svc/sessions/whoami"
		}
		if strings.Contains(variant, "cors") {
			s.Annotations[envoyAnnotationPrefix+"enable-cors"] = "true"
		}
		p, e := GenerateRoutePlan("fixture", s, nil, "main", routeConfig())
		if e != nil {
			t.Fatal(e)
		}
		for _, r := range p.Resources {
			objs = append(objs, r.Object.Object)
		}
		objs = append(objs, object{"apiVersion": "gateway.networking.k8s.io/v1", "kind": "Gateway", "metadata": object{"name": "fixture-" + variant, "namespace": p.GatewayNamespace}, "spec": object{"gatewayClassName": "edge", "listeners": []interface{}{p.Listener}}})
	}
	data, _ := json.MarshalIndent(objs, "", "  ")
	if e := os.WriteFile(path, data, 0600); e != nil {
		t.Fatal(e)
	}
}

func TestRouteAnnotationUpdates(t *testing.T) {
	s := publicService()
	s.Annotations = map[string]string{"example.com/owner": "old"}
	p, err := GenerateRoutePlan("app", s, nil, "main", routeConfig())
	if err != nil {
		t.Fatal(err)
	}
	oldClient := dynamicClient
	defer func() { dynamicClient = oldClient }()
	dynamicClient = dynamicfake.NewSimpleDynamicClient(runtime.NewScheme())
	route := resourceOf(t, p, httpRoutes)
	r := routeResource{GVR: httpRoutes, Object: route}
	if err := applyRouteResource(context.Background(), p, r); err != nil {
		t.Fatal(err)
	}
	route.SetAnnotations(map[string]string{"example.com/team": "new"})
	if err := applyRouteResource(context.Background(), p, r); err != nil {
		t.Fatal(err)
	}
	got, err := dynamicClient.Resource(httpRoutes).Namespace("app").Get(context.Background(), route.GetName(), metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got.GetAnnotations()["example.com/team"] != "new" || got.GetAnnotations()["example.com/owner"] != "" {
		t.Fatal(got.GetAnnotations())
	}
}
