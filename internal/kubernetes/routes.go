package kubernetes

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"nimbus/internal/config"
	"nimbus/internal/models"
	"nimbus/internal/utils"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation"
)

type object = map[string]interface{}

var (
	httpRoutes        = schema.GroupVersionResource{Group: "gateway.networking.k8s.io", Version: "v1", Resource: "httproutes"}
	grpcRoutes        = schema.GroupVersionResource{Group: "gateway.networking.k8s.io", Version: "v1", Resource: "grpcroutes"}
	gateways          = schema.GroupVersionResource{Group: "gateway.networking.k8s.io", Version: "v1", Resource: "gateways"}
	certificates      = schema.GroupVersionResource{Group: "cert-manager.io", Version: "v1", Resource: "certificates"}
	referenceGrants   = schema.GroupVersionResource{Group: "gateway.networking.k8s.io", Version: "v1beta1", Resource: "referencegrants"}
	securityPolicies  = schema.GroupVersionResource{Group: "gateway.envoyproxy.io", Version: "v1alpha1", Resource: "securitypolicies"}
	backendPolicies   = schema.GroupVersionResource{Group: "gateway.envoyproxy.io", Version: "v1alpha1", Resource: "backendtrafficpolicies"}
	extensionPolicies = schema.GroupVersionResource{Group: "gateway.envoyproxy.io", Version: "v1alpha1", Resource: "envoyextensionpolicies"}
	clientPolicies    = schema.GroupVersionResource{Group: "gateway.envoyproxy.io", Version: "v1alpha1", Resource: "clienttrafficpolicies"}
	configMaps        = schema.GroupVersionResource{Version: "v1", Resource: "configmaps"}
	services          = schema.GroupVersionResource{Version: "v1", Resource: "services"}
	deployments       = schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}
)

const managedBy = "app.kubernetes.io/managed-by"
const serviceLabel = "nimbus.dev/service"
const namespaceLabel = "nimbus.dev/namespace"

type routeResource struct {
	GVR    schema.GroupVersionResource
	Object *unstructured.Unstructured
}
type RoutePlan struct {
	Host, Namespace, ServiceName, ListenerName, GatewayName, GatewayNamespace, HTTPListener string
	Listener                                                                                object
	Resources                                                                               []routeResource
	RouteKind                                                                               schema.GroupVersionResource
	HasHelper                                                                               bool
}

func routingDefaults(c *config.Config) config.Config {
	v := config.Config{}
	if c != nil {
		v = *c
	}
	if v.GatewayName == "" {
		v.GatewayName = "edge"
	}
	if v.GatewayNamespace == "" {
		v.GatewayNamespace = "envoy-gateway-system"
	}
	if v.GatewayHTTPListener == "" {
		v.GatewayHTTPListener = "http"
	}
	if v.RouteReadyTimeout <= 0 {
		v.RouteReadyTimeout = 180
	}
	return v
}
func routeName(parts ...string) string {
	s := strings.Join(parts, "-")
	if len(s) <= 63 {
		return s
	}
	h := sha256.Sum256([]byte(s))
	return strings.TrimRight(s[:50], "-") + "-" + hex.EncodeToString(h[:6])
}
func ownedMetadata(ns, name, ownerNS, service string) object {
	return object{"name": name, "namespace": ns, "labels": object{managedBy: "nimbus", serviceLabel: service, namespaceLabel: ownerNS}}
}
func (p *RoutePlan) add(gvr schema.GroupVersionResource, kind, namespace, name string, spec object) {
	v := gvr.Version
	if gvr.Group != "" {
		v = gvr.Group + "/" + v
	}
	p.Resources = append(p.Resources, routeResource{gvr, &unstructured.Unstructured{Object: object{"apiVersion": v, "kind": kind, "metadata": ownedMetadata(namespace, name, p.Namespace, p.ServiceName), "spec": spec}}})
}

func GenerateRoutePlan(namespace string, s *models.Service, existingHost *string, branch string, cfg *config.Config) (*RoutePlan, error) {
	if s.Template != "http" || !s.Public {
		return nil, nil
	}
	if err := ValidateRoutePrerequisites(s, cfg); err != nil {
		return nil, err
	}
	o, err := routeSettings(s)
	if err != nil {
		return nil, err
	}
	c := routingDefaults(cfg)
	host := ""
	if s.Ingress != "" && utils.IsMainBranch(branch) {
		host = s.Ingress
	} else if existingHost != nil && *existingHost != "" {
		host = *existingHost
	} else {
		random, e := GenerateRandomChars()
		if e != nil {
			return nil, e
		}
		host = random + "." + c.Domain
	}
	if errs := validation.IsDNS1123Subdomain(host); len(errs) > 0 {
		return nil, fmt.Errorf("invalid public hostname %q: %s", host, strings.Join(errs, ", "))
	}
	p := &RoutePlan{Host: host, Namespace: namespace, ServiceName: s.Name, ListenerName: routeName("nimbus", namespace, s.Name), GatewayName: c.GatewayName, GatewayNamespace: c.GatewayNamespace, HTTPListener: c.GatewayHTTPListener, RouteKind: httpRoutes, HasHelper: o.SPA || o.AuthURL != ""}
	tls := s.Name + "-tls"
	name := routeName(s.Name, "route")
	p.Listener = object{"name": p.ListenerName, "hostname": host, "port": int64(443), "protocol": "HTTPS", "tls": object{"mode": "Terminate", "certificateRefs": []interface{}{object{"group": "", "kind": "Secret", "name": tls, "namespace": namespace}}}, "allowedRoutes": object{"namespaces": object{"from": "Selector", "selector": object{"matchLabels": object{"kubernetes.io/metadata.name": namespace}}}}}
	p.add(certificates, "Certificate", namespace, tls, object{"secretName": tls, "dnsNames": []interface{}{host}, "issuerRef": object{"name": o.Issuer, "kind": "ClusterIssuer"}})
	p.add(referenceGrants, "ReferenceGrant", namespace, routeName(s.Name, "edge-tls"), object{"from": []interface{}{object{"group": "gateway.networking.k8s.io", "kind": "Gateway", "namespace": c.GatewayNamespace}}, "to": []interface{}{object{"group": "", "kind": "Secret", "name": tls}}})
	backendName := s.Name
	backendPort := int64(80)
	if p.HasHelper {
		if c.RouteHelperImage == "" || !strings.Contains(c.RouteHelperImage, "@sha256:") {
			return nil, fmt.Errorf("NIMBUS_ROUTE_HELPER_IMAGE must pin a Nimbus image digest supporting route-helper for auth/spa services")
		}
		p.addHelper(s, o, c.RouteHelperImage)
		if o.SPA {
			backendName = routeName(s.Name, "route-helper")
			backendPort = 9001
		}
	}
	kind := "HTTPRoute"
	if o.GRPC {
		kind = "GRPCRoute"
		p.RouteKind = grpcRoutes
	}
	parent := object{"name": c.GatewayName, "namespace": c.GatewayNamespace, "sectionName": p.ListenerName}
	rule := object{"backendRefs": []interface{}{object{"name": backendName, "port": backendPort}}}
	if !o.GRPC {
		rule["matches"] = []interface{}{object{"path": object{"type": "PathPrefix", "value": "/"}}}
	}
	p.add(p.RouteKind, kind, namespace, name, object{"parentRefs": []interface{}{parent}, "hostnames": []interface{}{host}, "rules": []interface{}{rule}})
	annotations := map[string]string{}
	for key, value := range s.Annotations {
		if !strings.HasPrefix(key, "nginx.ingress.kubernetes.io/") && !strings.HasPrefix(key, "cert-manager.io/") {
			annotations[key] = value
		}
	}
	p.Resources[len(p.Resources)-1].Object.SetAnnotations(annotations)

	p.add(httpRoutes, "HTTPRoute", namespace, routeName(s.Name, "https-redirect"), object{"parentRefs": []interface{}{object{"name": c.GatewayName, "namespace": c.GatewayNamespace, "sectionName": c.GatewayHTTPListener}}, "hostnames": []interface{}{host}, "rules": []interface{}{object{"filters": []interface{}{object{"type": "RequestRedirect", "requestRedirect": object{"scheme": "https", "port": int64(443), "statusCode": int64(308)}}}}}})
	target := []interface{}{object{"group": "gateway.networking.k8s.io", "kind": kind, "name": name}}
	if o.AuthURL != "" || o.CORS != nil {
		spec := object{"targetRefs": target}
		if o.CORS != nil {
			spec["cors"] = o.CORS
		}
		if o.AuthURL != "" {
			http := object{"backendRefs": []interface{}{object{"name": routeName(s.Name, "route-helper"), "port": int64(9000)}}}
			if len(o.IdentityHeaders) > 0 {
				http["headersToBackend"] = strs(o.IdentityHeaders)
			}
			spec["extAuth"] = object{"failOpen": false, "headersToExtAuth": []interface{}{"Cookie", "Authorization"}, "http": http}
		}
		p.add(securityPolicies, "SecurityPolicy", namespace, name, spec)
	}
	// Remove untrusted identity headers before ext_authz, scoped to this listener only.
	remove := []string{"X-Auth-Request-User", "X-Auth-Request-Email"}
	seen := map[string]bool{"X-Auth-Request-User": true, "X-Auth-Request-Email": true}
	for _, h := range o.IdentityHeaders {
		if !seen[h] {
			remove = append(remove, h)
			seen[h] = true
		}
	}
	p.add(clientPolicies, "ClientTrafficPolicy", c.GatewayNamespace, p.ListenerName, object{"targetRefs": []interface{}{object{"group": "gateway.networking.k8s.io", "kind": "Gateway", "name": c.GatewayName, "sectionName": p.ListenerName}}, "headers": object{"earlyRequestHeaders": object{"remove": strs(remove)}}})
	p.add(backendPolicies, "BackendTrafficPolicy", namespace, name, object{"targetRefs": target, "timeout": object{"tcp": object{"connectTimeout": o.Connect}, "http": object{"requestTimeout": "0s", "streamIdleTimeout": o.Idle, "maxStreamDuration": "0s"}}})
	if !o.GRPC && o.BodyLimit > 0 {
		lua := fmt.Sprintf("function envoy_on_request(h)\n if h:headers():get('upgrade') ~= nil then return end\n local n=tonumber(h:headers():get('content-length') or '0')\n if n > %d then h:respond({[':status']='413'}, 'Request too large'); return end\n local b=h:body()\n if b ~= nil and b:length() > %d then h:respond({[':status']='413'}, 'Request too large') end\nend", o.BodyLimit, o.BodyLimit)
		p.add(extensionPolicies, "EnvoyExtensionPolicy", namespace, name, object{"targetRefs": target, "lua": []interface{}{object{"type": "Inline", "inline": lua}}})
	}
	return p, nil
}

func (p *RoutePlan) addHelper(s *models.Service, o routeOptions, image string) {
	name := routeName(s.Name, "route-helper")
	cfg := object{"auth": object{}, "spa": object{}, "connect_timeout": o.Connect, "response_header_timeout": o.Idle}
	if o.AuthURL != "" {
		cfg["auth"].(object)[p.Host] = object{
			"url": o.AuthURL, "sign_in": o.SignIn, "identity_headers": strs(o.IdentityHeaders),
		}
	}
	if o.SPA {
		cfg["spa"].(object)[p.Host] = fmt.Sprintf("http://%s.%s.svc.cluster.local:80", s.Name, p.Namespace)
	}
	data, _ := json.Marshal(cfg)
	hash := sha256.Sum256(data)
	p.Resources = append(p.Resources, routeResource{configMaps, &unstructured.Unstructured{Object: object{
		"apiVersion": "v1", "kind": "ConfigMap",
		"metadata": ownedMetadata(p.Namespace, name, p.Namespace, s.Name),
		"data":     object{"config.json": string(data)},
	}}})
	labels := object{"app.kubernetes.io/name": name}
	container := object{
		"name": "helper", "image": image, "args": []interface{}{"route-helper"},
		"ports": []interface{}{object{"containerPort": int64(9000)}, object{"containerPort": int64(9001)}, object{"containerPort": int64(9002)}},
		"securityContext": object{
			"allowPrivilegeEscalation": false, "readOnlyRootFilesystem": true,
			"capabilities": object{"drop": []interface{}{"ALL"}},
		},
		"resources": object{
			"requests": object{"cpu": "25m", "memory": "32Mi"}, "limits": object{"memory": "256Mi"},
		},
		"readinessProbe": object{"httpGet": object{"path": "/healthz", "port": int64(9002)}},
		"livenessProbe":  object{"httpGet": object{"path": "/healthz", "port": int64(9002)}},
		"volumeMounts":   []interface{}{object{"name": "config", "mountPath": "/config", "readOnly": true}},
	}
	pod := object{
		"automountServiceAccountToken": false,
		"securityContext": object{
			"runAsNonRoot": true, "runAsUser": int64(65532), "runAsGroup": int64(65532),
			"seccompProfile": object{"type": "RuntimeDefault"},
		},
		"containers": []interface{}{container},
		"volumes":    []interface{}{object{"name": "config", "configMap": object{"name": name}}},
	}
	p.add(deployments, "Deployment", p.Namespace, name, object{
		"replicas": int64(2), "selector": object{"matchLabels": labels},
		"template": object{
			"metadata": object{"labels": labels, "annotations": object{"nimbus.dev/config-hash": hex.EncodeToString(hash[:])}},
			"spec":     pod,
		},
	})
	p.add(services, "Service", p.Namespace, name, object{
		"type": "ClusterIP", "selector": labels,
		"ports": []interface{}{
			object{"name": "auth", "port": int64(9000), "targetPort": int64(9000)},
			object{"name": "spa", "port": int64(9001), "targetPort": int64(9001)},
		},
	})
}
