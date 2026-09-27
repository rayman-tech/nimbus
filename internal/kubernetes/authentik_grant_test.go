package kubernetes

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"nimbus/internal/models"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/validation"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	kubefake "k8s.io/client-go/kubernetes/fake"
	ktesting "k8s.io/client-go/testing"
)

func authentikPlan(t *testing.T, namespace, service, branch string) *RoutePlan {
	t.Helper()
	s := publicService()
	s.Name = service
	s.Auth = &models.Auth{Provider: "authentik"}
	p, err := GenerateRoutePlan(namespace, s, nil, branch, routeConfig())
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func authentikGrant(t *testing.T, p *RoutePlan) routeResource {
	t.Helper()
	for _, r := range p.Resources {
		if r.GVR == referenceGrants && r.Object.GetNamespace() == authentikNamespace {
			return r
		}
	}
	t.Fatal("missing Authentik grant")
	return routeResource{}
}

func fakeAuthentikClients(t *testing.T, namespace string, objects ...runtime.Object) {
	t.Helper()
	previousDynamic, previousClient := dynamicClient, client
	var resources []runtime.Object
	var gatewayObjects []*unstructured.Unstructured
	for _, obj := range objects {
		if u, ok := obj.(*unstructured.Unstructured); ok && u.GetKind() == "Gateway" {
			gatewayObjects = append(gatewayObjects, u)
		} else {
			resources = append(resources, obj)
		}
	}
	dc := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), resources...)
	// The fake client's default pluralization guesses "gatewaies".
	for _, gateway := range gatewayObjects {
		if err := dc.Tracker().Create(gateways, gateway, gateway.GetNamespace()); err != nil {
			t.Fatal(err)
		}
	}
	dynamicClient = dc
	client = kubefake.NewSimpleClientset(&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace, UID: types.UID("namespace-uid")}})
	t.Cleanup(func() { dynamicClient, client = previousDynamic, previousClient })
}

func TestAuthentikGrantScopeAndNames(t *testing.T) {
	for _, branch := range []string{"main", "feature/login"} {
		p := authentikPlan(t, "dashboard-preview", "server", branch)
		grant := authentikGrant(t, p).Object
		want := object{
			"from": []interface{}{
				object{"group": "gateway.envoyproxy.io", "kind": "SecurityPolicy", "namespace": p.Namespace},
				object{"group": "gateway.networking.k8s.io", "kind": "HTTPRoute", "namespace": p.Namespace},
			},
			"to": []interface{}{object{"group": "", "kind": "Service", "name": authentikService}},
		}
		if !reflect.DeepEqual(grant.Object["spec"], want) || !ownedBy(grant, p.Namespace, p.ServiceName) {
			t.Fatalf("incorrect grant scope or ownership: %v", grant.Object)
		}
	}
	names := map[string]bool{}
	for _, pair := range [][2]string{{"app-web", "server"}, {"app", "web-server"}, {strings.Repeat("a", 63), strings.Repeat("b", 63)}} {
		name := authentikGrantName(pair[0], pair[1])
		if names[name] || len(validation.IsDNS1123Label(name)) != 0 || name != authentikGrantName(pair[0], pair[1]) {
			t.Fatalf("grant name must be unique, deterministic and DNS-safe: %s", name)
		}
		names[name] = true
	}
	p, err := GenerateRoutePlan("app", publicService(), nil, "feature", routeConfig())
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range p.Resources {
		if r.GVR == referenceGrants && r.Object.GetNamespace() == authentikNamespace {
			t.Fatal("unauthenticated service received Authentik permissions")
		}
	}
}

func TestAuthentikGrantLifecycle(t *testing.T) {
	for _, action := range []string{"disable-auth", "delete-service"} {
		t.Run(action, func(t *testing.T) {
			ctx := context.Background()
			p := authentikPlan(t, "app-preview", "web", "feature")
			otherService := authentikPlan(t, p.Namespace, "api", "feature")
			otherBranch := authentikPlan(t, "app-other-preview", "web", "other")
			shared := &unstructured.Unstructured{Object: object{"apiVersion": "gateway.networking.k8s.io/v1beta1", "kind": "ReferenceGrant", "metadata": object{"namespace": authentikNamespace, "name": "admin-managed"}}}
			other := authentikGrant(t, otherService).Object
			branch := authentikGrant(t, otherBranch).Object
			fakeAuthentikClients(t, p.Namespace, fakeGateway(p), shared, other, branch)
			grant := authentikGrant(t, p)
			for i := 0; i < 2; i++ {
				if err := applyRouteResource(ctx, p, grant); err != nil {
					t.Fatal(err)
				}
			}
			api := dynamicClient.Resource(referenceGrants).Namespace(authentikNamespace)
			got, err := api.Get(ctx, grant.Object.GetName(), metav1.GetOptions{})
			if err != nil {
				t.Fatal(err)
			}
			owners := got.GetOwnerReferences()
			if len(owners) != 1 || owners[0].APIVersion != "v1" || owners[0].Kind != "Namespace" || owners[0].Name != p.Namespace || owners[0].UID != "namespace-uid" {
				t.Fatalf("grant lacks namespace garbage-collection ownership: %v", owners)
			}
			if err := pruneRouteResources(ctx, p); err != nil {
				t.Fatal(err)
			}
			if _, err := api.Get(ctx, got.GetName(), metav1.GetOptions{}); err != nil {
				t.Fatalf("enabled grant pruned: %v", err)
			}
			if err := reconcileListener(ctx, p, false); err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 2; i++ {
				if action == "disable-auth" {
					disabled, err := GenerateRoutePlan(p.Namespace, publicService(), &p.Host, "feature", routeConfig())
					if err != nil {
						t.Fatal(err)
					}
					err = pruneRouteResources(ctx, disabled)
					if err != nil {
						t.Fatal(err)
					}
				} else if err := DeletePublicRoute(ctx, p.Namespace, p.ServiceName, routeConfig()); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := api.Get(ctx, got.GetName(), metav1.GetOptions{}); !apierrors.IsNotFound(err) {
				t.Fatalf("grant retained after %s: %v", action, err)
			}
			for _, original := range []*unstructured.Unstructured{shared, other, branch} {
				remaining, err := api.Get(ctx, original.GetName(), metav1.GetOptions{})
				if err != nil || !reflect.DeepEqual(remaining, original) {
					t.Fatalf("unrelated grant changed: %s: %v", original.GetName(), err)
				}
			}
		})
	}
}

func TestAuthentikGrantRefusesUnmanagedCollision(t *testing.T) {
	p := authentikPlan(t, "app-preview", "web", "feature")
	r := authentikGrant(t, p)
	unmanaged := r.Object.DeepCopy()
	unmanaged.SetLabels(nil)
	fakeAuthentikClients(t, p.Namespace, unmanaged)
	ctx := context.Background()
	if err := applyRouteResource(ctx, p, r); err == nil {
		t.Fatal("overwrote unmanaged grant")
	}
	if err := deleteOwned(ctx, referenceGrants, authentikNamespace, unmanaged.GetName(), p.Namespace, p.ServiceName); err == nil {
		t.Fatal("deleted unmanaged grant")
	}
}

func TestAuthentikGrantRequiresLiveOwnerNamespace(t *testing.T) {
	for _, missing := range []bool{false, true} {
		name := "terminating"
		if missing {
			name = "missing"
		}
		t.Run(name, func(t *testing.T) {
			p := authentikPlan(t, "app-preview", "web", "feature")
			fakeAuthentikClients(t, p.Namespace)
			ctx := context.Background()
			if missing {
				if err := client.CoreV1().Namespaces().Delete(ctx, p.Namespace, metav1.DeleteOptions{}); err != nil {
					t.Fatal(err)
				}
			} else {
				now := metav1.Now()
				ns, err := GetNamespace(ctx, p.Namespace)
				if err != nil {
					t.Fatal(err)
				}
				ns.DeletionTimestamp = &now
				if _, err := client.CoreV1().Namespaces().Update(ctx, ns, metav1.UpdateOptions{}); err != nil {
					t.Fatal(err)
				}
			}
			r := authentikGrant(t, p)
			if err := applyRouteResource(ctx, p, r); err == nil {
				t.Fatal("created grant without a live owner namespace")
			}
			if _, err := dynamicClient.Resource(referenceGrants).Namespace(authentikNamespace).Get(ctx, r.Object.GetName(), metav1.GetOptions{}); !apierrors.IsNotFound(err) {
				t.Fatal("orphaned grant exists", err)
			}
		})
	}
}

func TestAuthentikGrantAppliedBeforeAuthRoutes(t *testing.T) {
	p := authentikPlan(t, "app-preview", "web", "feature")
	var objects []runtime.Object
	for _, r := range p.Resources {
		if r.GVR != referenceGrants {
			objects = append(objects, readyResource(r, p))
		}
	}
	objects = append(objects, fakeGateway(p))
	fakeAuthentikClients(t, p.Namespace, objects...)
	dc := dynamicClient.(*dynamicfake.FakeDynamicClient)
	checked := false
	dc.PrependReactor("update", "httproutes", func(action ktesting.Action) (bool, runtime.Object, error) {
		if action.(ktesting.UpdateAction).GetObject().(*unstructured.Unstructured).GetName() == "web-authentik" {
			// Use the tracker directly to avoid reentering the fake client's lock.
			if _, err := dc.Tracker().Get(referenceGrants, authentikNamespace, authentikGrantName(p.Namespace, p.ServiceName)); err != nil {
				t.Error("callback applied before Authentik grant", err)
			}
			checked = true
		}
		return false, nil, nil
	})
	s := publicService()
	s.Auth = &models.Auth{Provider: "authentik"}
	if _, err := ReconcilePublicRoute(context.Background(), p.Namespace, s, &p.Host, "feature", routeConfig()); err != nil {
		t.Fatal(err)
	}
	if !checked {
		t.Fatal("callback route was not reconciled")
	}
}

func TestRouteReadinessReportsDeniedReference(t *testing.T) {
	for _, kind := range []string{"HTTPRoute", "SecurityPolicy"} {
		t.Run(kind, func(t *testing.T) {
			p := authentikPlan(t, "app-preview", "web", "feature")
			var denied routeResource
			for _, r := range p.Resources {
				if r.Object.GetKind() == kind && (kind != "HTTPRoute" || r.Object.GetName() == "web-authentik") {
					denied = r
				}
			}
			obj := readyResource(denied, p)
			key, ref, condition := "ancestors", "ancestorRef", "Accepted"
			if kind == "HTTPRoute" {
				key, ref, condition = "parents", "parentRef", "ResolvedRefs"
			}
			obj.Object["status"] = object{key: []interface{}{object{ref: object{"name": p.GatewayName, "namespace": p.GatewayNamespace}, "conditions": []interface{}{
				object{"type": condition, "status": "False", "observedGeneration": int64(1), "reason": "RefNotPermitted", "message": "cross-namespace reference denied"},
			}}}}
			p.Resources = []routeResource{denied}
			fakeAuthentikClients(t, p.Namespace, obj)
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
			defer cancel()
			err := waitRouteResources(ctx, p)
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("expected readiness timeout: %v", err)
			}
			for _, want := range []string{kind, p.Namespace + "/" + obj.GetName(), "RefNotPermitted", "cross-namespace reference denied"} {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("missing %q in readiness error: %v", want, err)
				}
			}
		})
	}
}
