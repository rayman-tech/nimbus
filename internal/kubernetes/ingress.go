// Package kubernetes contains functions for interacting with kubernetes
package kubernetes

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"nimbus/internal/config"
	"nimbus/internal/models"
	"nimbus/internal/utils"

	"k8s.io/apimachinery/pkg/api/errors"

	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func GenerateIngressSpec(namespace string, service *models.Service,
	existingIngress *string, branch string, cfg *config.Config,
) (*networkingv1.Ingress, error) {
	if service.Template != "http" || !service.Public {
		return nil, nil
	}

	// Determine ingress host:
	// 1. If a custom ingress domain is specified and we're on main/master, use it
	// 2. If an existing ingress host is already assigned, preserve it
	// 3. Otherwise generate a random subdomain
	isMainBranch := utils.IsMainBranch(branch)
	var host string
	if service.Ingress != "" && isMainBranch {
		host = service.Ingress
	} else if existingIngress != nil {
		host = *existingIngress
	} else {
		randChars, err := GenerateRandomChars()
		if err != nil {
			return nil, err
		}
		host = fmt.Sprintf("%s.%s", randChars, cfg.Domain)
	}

	spec := networkingv1.IngressSpec{
		Rules: []networkingv1.IngressRule{
			{
				Host: host,
				IngressRuleValue: networkingv1.IngressRuleValue{
					HTTP: &networkingv1.HTTPIngressRuleValue{
						Paths: []networkingv1.HTTPIngressPath{
							{
								Path: "/",
								PathType: func() *networkingv1.PathType {
									pt := networkingv1.PathTypePrefix
									return &pt
								}(),
								Backend: networkingv1.IngressBackend{
									Service: &networkingv1.IngressServiceBackend{
										Name: service.Name,
										Port: networkingv1.ServiceBackendPort{
											Number: defaultHTTPPort,
										},
									},
								},
							},
						},
					},
				},
			},
		},
		TLS: []networkingv1.IngressTLS{
			{
				Hosts: []string{
					host,
				},
				SecretName: fmt.Sprintf("%s-%s", service.Name, "tls"),
			},
		},
	}

	annotations := map[string]string{
		"created": time.Now().Format(time.RFC3339),
		"nginx.ingress.kubernetes.io/ssl-redirect":      "true",
		"nginx.ingress.kubernetes.io/cors-allow-origin": "*",
		"cert-manager.io/cluster-issuer":                "letsencrypt-prod",
	}

	for _, feature := range service.Features {
		switch feature {
		case "spa":
			annotations["nginx.ingress.kubernetes.io/configuration-snippet"] = "proxy_intercept_errors on;\nerror_page 404 =200 /index.html;\n"
		case "grpc":
			annotations["nginx.ingress.kubernetes.io/backend-protocol"] = "GRPC"
		}
	}

	return &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-%s", service.Name, "ingress"),
			Namespace: namespace,
			Annotations: annotations,
		},
		Spec: spec,
	}, nil
}

func CreateIngress(
	ctx context.Context, namespace string, ingress *networkingv1.Ingress,
) (*networkingv1.Ingress, error) {
	client := getClient().NetworkingV1().Ingresses(namespace)

	existing, err := client.Get(ctx, ingress.Name, metav1.GetOptions{})
	if errors.IsNotFound(err) {
		created, err := client.Create(ctx, ingress, metav1.CreateOptions{})
		if err != nil {
			return nil, fmt.Errorf("creating ingress: %w", err)
		}
		return created, nil
	}
	if err != nil {
		return nil, fmt.Errorf("getting ingress: %w", err)
	}

	existing.Spec = ingress.Spec
	existing.Annotations = ingress.Annotations

	updated, err := client.Update(ctx, existing, metav1.UpdateOptions{})
	if err != nil {
		return nil, fmt.Errorf("updating ingress: %w", err)
	}
	return updated, nil
}

func DeleteIngress(ctx context.Context, namespace, host string) error {
	ingresses, err := getClient().NetworkingV1().Ingresses(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("listing ingress: %w", err)
	}

	for _, ingress := range ingresses.Items {
		for _, rule := range ingress.Spec.Rules {
			if rule.Host == host {
				err := getClient().NetworkingV1().Ingresses(namespace).Delete(
					ctx, ingress.Name, metav1.DeleteOptions{})
				if err != nil && !errors.IsNotFound(err) {
					return fmt.Errorf("deleting ingress %s: %w", ingress.Name, err)
				}
				return nil
			}
		}
	}

	return nil
}

func GenerateRandomChars() (string, error) {
	const numBytes = 8
	randBytes := make([]byte, numBytes)
	_, err := rand.Read(randBytes)
	if err != nil {
		return "", fmt.Errorf("generating random chars: %w", err)
	}
	return hex.EncodeToString(randBytes), nil
}
