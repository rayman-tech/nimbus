package kubernetes

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"time"

	"nimbus/internal/config"
	"nimbus/internal/models"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/util/retry"
)

func ownedBy(u *unstructured.Unstructured, ns, service string) bool {
	l := u.GetLabels()
	return l[managedBy] == "nimbus" && l[namespaceLabel] == ns && l[serviceLabel] == service
}

func applyRouteResource(ctx context.Context, p *RoutePlan, r routeResource) error {
	api := dynamicClient.Resource(r.GVR).Namespace(r.Object.GetNamespace())
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		old, e := api.Get(ctx, r.Object.GetName(), metav1.GetOptions{})
		if apierrors.IsNotFound(e) {
			_, e = api.Create(ctx, r.Object.DeepCopy(), metav1.CreateOptions{})
			return e
		}
		if e != nil {
			return e
		}
		if !ownedBy(old, p.Namespace, p.ServiceName) {
			if r.GVR != certificates {
				return fmt.Errorf("refusing to overwrite unmanaged %s/%s", r.Object.GetKind(), r.Object.GetName())
			}
			secret, _, _ := unstructured.NestedString(old.Object, "spec", "secretName")
			domains, _, _ := unstructured.NestedStringSlice(old.Object, "spec", "dnsNames")
			legitimate := len(domains) == 1 && domains[0] == p.Host
			for _, owner := range old.GetOwnerReferences() {
				if owner.Kind == "Ingress" && owner.Name == p.ServiceName+"-ingress" {
					legitimate = true
				}
			}
			if secret != p.ServiceName+"-tls" || !legitimate {
				return fmt.Errorf("refusing to adopt unrelated certificate %s", old.GetName())
			}
		}
		desired := r.Object.DeepCopy()
		labels := old.GetLabels()
		if labels == nil {
			labels = map[string]string{}
		}
		for k, v := range desired.GetLabels() {
			labels[k] = v
		}
		old.SetLabels(labels)
		// These resources are owned by Nimbus; replace route metadata as configuration changes.
		if r.GVR == httpRoutes || r.GVR == grpcRoutes {
			old.SetAnnotations(desired.GetAnnotations())
		}
		if r.GVR == configMaps {
			old.Object["data"] = desired.Object["data"]
		} else if r.GVR == certificates {
			// Preserve private-key/renewal settings and detach only this service's legacy owner.
			for _, field := range []string{"secretName", "dnsNames", "issuerRef"} {
				v, _, _ := unstructured.NestedFieldCopy(desired.Object, "spec", field)
				_ = unstructured.SetNestedField(old.Object, v, "spec", field)
			}
			refs := old.GetOwnerReferences()
			keep := refs[:0]
			for _, o := range refs {
				if !(o.Kind == "Ingress" && o.Name == p.ServiceName+"-ingress") {
					keep = append(keep, o)
				}
			}
			old.SetOwnerReferences(keep)
		} else {
			if r.GVR == services {
				for _, field := range []string{"clusterIP", "clusterIPs", "ipFamilies", "ipFamilyPolicy"} {
					if v, ok, _ := unstructured.NestedFieldCopy(old.Object, "spec", field); ok {
						_ = unstructured.SetNestedField(desired.Object, v, "spec", field)
					}
				}
			}
			old.Object["spec"] = desired.Object["spec"]
		}
		_, e = api.Update(ctx, old, metav1.UpdateOptions{})
		return e
	})
}

func reconcileListener(ctx context.Context, p *RoutePlan, remove bool) error {
	api := dynamicClient.Resource(gateways).Namespace(p.GatewayNamespace)
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		gateway, e := api.Get(ctx, p.GatewayName, metav1.GetOptions{})
		if apierrors.IsNotFound(e) && remove {
			return nil
		}
		if e != nil {
			return e
		}
		annotations := gateway.GetAnnotations()
		if annotations == nil {
			annotations = map[string]string{}
		}
		owners := map[string]string{}
		if raw := annotations["nimbus.dev/listener-owners"]; raw != "" {
			if err := json.Unmarshal([]byte(raw), &owners); err != nil {
				return fmt.Errorf("invalid Gateway listener ownership annotation")
			}
		}
		owner := p.Namespace + "/" + p.ServiceName
		listeners, _, _ := unstructured.NestedSlice(gateway.Object, "spec", "listeners")
		out := make([]interface{}, 0, len(listeners)+1)
		foundHTTP := false
		found := false
		for _, raw := range listeners {
			l := raw.(map[string]interface{})
			if l["name"] == p.HTTPListener && l["protocol"] == "HTTP" {
				foundHTTP = true
			}
			if l["name"] == p.ListenerName {
				if owners[p.ListenerName] != owner {
					return fmt.Errorf("refusing to change an unowned Gateway listener %s", p.ListenerName)
				}
				found = true
				if !remove {
					out = append(out, p.Listener)
				}
				continue
			}
			if !remove && l["hostname"] == p.Host {
				return fmt.Errorf("hostname %s already belongs to another Gateway listener", p.Host)
			}
			out = append(out, raw)
		}
		if !remove {
			if !foundHTTP {
				return fmt.Errorf("Gateway HTTP listener %q is missing", p.HTTPListener)
			}
			if !found {
				out = append(out, p.Listener)
			}
			if len(out) > 64 {
				return fmt.Errorf("Gateway listener limit reached; provision another configured Gateway")
			}
		}
		if reflect.DeepEqual(out, listeners) {
			return nil
		}
		if remove {
			delete(owners, p.ListenerName)
		} else {
			owners[p.ListenerName] = owner
		}
		encoded, _ := json.Marshal(owners)
		annotations["nimbus.dev/listener-owners"] = string(encoded)
		gateway.SetAnnotations(annotations)
		_ = unstructured.SetNestedSlice(gateway.Object, out, "spec", "listeners")
		_, e = api.Update(ctx, gateway, metav1.UpdateOptions{})
		return e
	})
}

func readyCondition(conditions []interface{}, kind string, generation int64) bool {
	for _, raw := range conditions {
		c := raw.(map[string]interface{})
		if c["type"] != kind || c["status"] != "True" {
			continue
		}
		if observed, ok := c["observedGeneration"].(int64); ok && observed != generation {
			continue
		}
		return true
	}
	return false
}
func waitRouteResources(ctx context.Context, p *RoutePlan) error {
	return wait.PollUntilContextCancel(ctx, 500*time.Millisecond, true, func(ctx context.Context) (bool, error) {
		for _, r := range p.Resources {
			if r.GVR != p.RouteKind && r.GVR != httpRoutes && r.GVR != certificates && r.GVR != securityPolicies && r.GVR != backendPolicies && r.GVR != extensionPolicies && r.GVR != clientPolicies && r.GVR != deployments {
				continue
			}
			obj, e := dynamicClient.Resource(r.GVR).Namespace(r.Object.GetNamespace()).Get(ctx, r.Object.GetName(), metav1.GetOptions{})
			if e != nil {
				return false, e
			}
			if r.GVR == deployments {
				available, _, _ := unstructured.NestedInt64(obj.Object, "status", "availableReplicas")
				observed, _, _ := unstructured.NestedInt64(obj.Object, "status", "observedGeneration")
				if available < 2 || observed != obj.GetGeneration() {
					return false, nil
				}
				continue
			}
			if r.GVR == certificates {
				c, _, _ := unstructured.NestedSlice(obj.Object, "status", "conditions")
				if !readyCondition(c, "Ready", obj.GetGeneration()) {
					return false, nil
				}
				continue
			}
			key := "ancestors"
			isRoute := r.GVR == httpRoutes || r.GVR == grpcRoutes
			if isRoute {
				key = "parents"
			}
			entries, _, _ := unstructured.NestedSlice(obj.Object, "status", key)
			accepted := false
			for _, raw := range entries {
				entry := raw.(map[string]interface{})
				refkey := "ancestorRef"
				if isRoute {
					refkey = "parentRef"
				}
				ref, _ := entry[refkey].(map[string]interface{})
				if ref["name"] != p.GatewayName {
					continue
				}
				if n, ok := ref["namespace"].(string); ok && n != p.GatewayNamespace {
					continue
				}
				cs, _ := entry["conditions"].([]interface{})
				if !readyCondition(cs, "Accepted", obj.GetGeneration()) {
					continue
				}
				if isRoute && !readyCondition(cs, "ResolvedRefs", obj.GetGeneration()) {
					continue
				}
				bad := false
				for _, v := range cs {
					c := v.(map[string]interface{})
					if c["status"] == "False" && (c["type"] == "Accepted" || c["type"] == "ResolvedRefs" || c["type"] == "Programmed" || c["type"] == "BackendsAvailable") {
						bad = true
					}
				}
				if !bad {
					accepted = true
				}
			}
			if !accepted {
				return false, nil
			}
		}
		return true, nil
	})
}

// ReconcilePublicRoute does not retire an old ingress until certificates, routes,
// helpers and policies have been accepted. The DB continues to store the hostname.
func ReconcilePublicRoute(ctx context.Context, namespace string, s *models.Service, existing *string, branch string, cfg *config.Config) (string, error) {
	c := routingDefaults(cfg)
	if existing == nil {
		// Recover the hostname after a partial deployment before the DB was updated.
		for _, gvr := range []schema.GroupVersionResource{httpRoutes, grpcRoutes} {
			u, e := dynamicClient.Resource(gvr).Namespace(namespace).Get(ctx, routeName(s.Name, "route"), metav1.GetOptions{})
			if e == nil && ownedBy(u, namespace, s.Name) {
				h, _, _ := unstructured.NestedStringSlice(u.Object, "spec", "hostnames")
				if len(h) == 1 {
					existing = &h[0]
					break
				}
			} else if e != nil && !apierrors.IsNotFound(e) {
				return "", e
			}
		}
	}
	p, e := GenerateRoutePlan(namespace, s, existing, branch, &c)
	if e != nil {
		return "", e
	}
	if p == nil {
		return "", fmt.Errorf("public HTTP service required")
	}
	deadline, cancel := context.WithTimeout(ctx, time.Duration(c.RouteReadyTimeout)*time.Second)
	defer cancel()
	// Install policy/helper objects first. Missing or invalid auth fails closed in Envoy.
	for _, r := range p.Resources {
		if r.GVR == httpRoutes || r.GVR == grpcRoutes {
			continue
		}
		if e = applyRouteResource(deadline, p, r); e != nil {
			return "", fmt.Errorf("applying %s: %w", r.Object.GetKind(), e)
		}
	}
	// A protocol switch must not leave a competing route attached to this hostname.
	opposite := grpcRoutes
	if p.RouteKind == grpcRoutes {
		opposite = httpRoutes
	}
	if e = deleteOwned(deadline, opposite, namespace, routeName(s.Name, "route"), namespace, s.Name); e != nil {
		return "", e
	}
	for _, r := range p.Resources {
		if r.GVR != httpRoutes && r.GVR != grpcRoutes {
			continue
		}
		if e = applyRouteResource(deadline, p, r); e != nil {
			return "", e
		}
	}
	if e = reconcileListener(deadline, p, false); e != nil {
		return "", e
	}
	if e = pruneRouteResources(deadline, p); e != nil {
		return "", e
	}
	if e = waitRouteResources(deadline, p); e != nil {
		return "", fmt.Errorf("Envoy route not ready; legacy ingress retained: %w", e)
	}
	if e = deleteLegacyIngress(deadline, namespace, s.Name); e != nil {
		return "", e
	}
	return p.Host, nil
}

func deleteOwned(ctx context.Context, gvr schema.GroupVersionResource, ns, name, ownerNS, service string) error {
	api := dynamicClient.Resource(gvr).Namespace(ns)
	o, e := api.Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(e) {
		return nil
	}
	if e != nil {
		return e
	}
	if !ownedBy(o, ownerNS, service) {
		return fmt.Errorf("refusing to delete unmanaged %s/%s", gvr.Resource, name)
	}
	uid := o.GetUID()
	e = api.Delete(ctx, name, metav1.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &uid}})
	if apierrors.IsNotFound(e) {
		return nil
	}
	return e
}
func pruneRouteResources(ctx context.Context, p *RoutePlan) error {
	wanted := map[string]bool{}
	for _, r := range p.Resources {
		wanted[r.GVR.Resource+"/"+r.Object.GetName()] = true
	}
	for _, gvr := range []schema.GroupVersionResource{securityPolicies, extensionPolicies, deployments, services, configMaps} {
		name := routeName(p.ServiceName, "route")
		if gvr == deployments || gvr == services || gvr == configMaps {
			name = routeName(p.ServiceName, "route-helper")
		}
		if !wanted[gvr.Resource+"/"+name] {
			if e := deleteOwned(ctx, gvr, p.Namespace, name, p.Namespace, p.ServiceName); e != nil {
				return e
			}
		}
	}
	return nil
}
func deleteLegacyIngress(ctx context.Context, namespace, service string) error {
	api := getClient().NetworkingV1().Ingresses(namespace)
	old, e := api.Get(ctx, service+"-ingress", metav1.GetOptions{})
	if apierrors.IsNotFound(e) {
		return nil
	}
	if e != nil {
		return e
	}
	// Only Nimbus's predictable service ingress, and never an unrelated backend.
	for _, r := range old.Spec.Rules {
		if r.HTTP == nil {
			continue
		}
		for _, path := range r.HTTP.Paths {
			if path.Backend.Service == nil || path.Backend.Service.Name != service {
				return fmt.Errorf("legacy ingress has an unrelated backend")
			}
		}
	}
	policy := metav1.DeletePropagationOrphan
	uid := old.UID
	e = api.Delete(ctx, old.Name, metav1.DeleteOptions{PropagationPolicy: &policy, Preconditions: &metav1.Preconditions{UID: &uid}})
	if apierrors.IsNotFound(e) {
		return nil
	}
	return e
}
func DeletePublicRoute(ctx context.Context, namespace, service string, cfg *config.Config) error {
	c := routingDefaults(cfg)
	p := &RoutePlan{Namespace: namespace, ServiceName: service, GatewayName: c.GatewayName, GatewayNamespace: c.GatewayNamespace, HTTPListener: c.GatewayHTTPListener, ListenerName: routeName("nimbus", namespace, service)}
	// Remove only this service's listener; other applications on the Gateway remain intact.
	if e := reconcileListener(ctx, p, true); e != nil {
		return e
	}
	for _, r := range []struct {
		g        schema.GroupVersionResource
		ns, name string
	}{
		{httpRoutes, namespace, routeName(service, "route")}, {grpcRoutes, namespace, routeName(service, "route")}, {httpRoutes, namespace, routeName(service, "https-redirect")},
		{securityPolicies, namespace, routeName(service, "route")}, {backendPolicies, namespace, routeName(service, "route")}, {extensionPolicies, namespace, routeName(service, "route")},
		{clientPolicies, c.GatewayNamespace, p.ListenerName}, {referenceGrants, namespace, routeName(service, "edge-tls")},
		{deployments, namespace, routeName(service, "route-helper")}, {services, namespace, routeName(service, "route-helper")}, {configMaps, namespace, routeName(service, "route-helper")},
	} {
		if e := deleteOwned(ctx, r.g, r.ns, r.name, namespace, service); e != nil {
			return e
		}
	}
	// Retain certificates/keys for a subsequent redeploy; namespace deletion removes them.
	return deleteLegacyIngress(ctx, namespace, service)
}
