// Package kubernetes contains functions for interacting with kubernetes
package kubernetes

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"time"

	"nimbus/internal/config"
	"nimbus/internal/env"

	"k8s.io/apimachinery/pkg/api/errors"

	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func GenerateIngressSpec(namespace string, service *config.Service,
	existingIngress *string, env *env.Env,
) (*networkingv1.Ingress, error) {
	if service.Template != "http" || !service.Public {
		return nil, nil
	}

	randomString := GenerateRandomChars()
	spec := networkingv1.IngressSpec{
		Rules: []networkingv1.IngressRule{
			{
				Host: fmt.Sprintf("%s.%s", randomString, os.Getenv("DOMAIN")),
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
					fmt.Sprintf("%s.%s", randomString, os.Getenv("DOMAIN")),
				},
				SecretName: fmt.Sprintf("%s-%s", service.Name, "tls"),
			},
		},
	}
	if existingIngress != nil {
		spec.Rules[0].Host = *existingIngress
	}

	return &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-%s", service.Name, "ingress"),
			Namespace: namespace,
			Annotations: map[string]string{
				"created": time.Now().Format(time.RFC3339),
				"nginx.ingress.kubernetes.io/rewrite-target":    "/",
				"nginx.ingress.kubernetes.io/ssl-redirect":      "true",
				"nginx.ingress.kubernetes.io/cors-allow-origin": "*",
				"cert-manager.io/cluster-issuer":                "letsencrypt-prod",
			},
		},
		Spec: spec,
	}, nil
}

func CreateIngress(
	ctx context.Context, namespace string, ingress *networkingv1.Ingress, env *env.Env,
) (*networkingv1.Ingress, error) {
	_, err := getClient(env).NetworkingV1().Ingresses(namespace).Create(
		ctx, ingress, metav1.CreateOptions{})
	if errors.IsAlreadyExists(err) {
		return ingress, nil
	}
	if err != nil {
		return nil, err
	}
	return ingress, nil
}

func DeleteIngress(ctx context.Context, namespace, host string, env *env.Env) error {
	ingresses, err := getClient(env).NetworkingV1().Ingresses(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("listing ingress: %w", err)
	}

	for _, ingress := range ingresses.Items {
		for _, rule := range ingress.Spec.Rules {
			if rule.Host == host {
				err := getClient(env).NetworkingV1().Ingresses(namespace).Delete(
					ctx, ingress.Name, metav1.DeleteOptions{})
				if err != nil {
					return fmt.Errorf("deleting ingress %s: %w", ingress.Name, err)
				}
				return nil
			}
		}
	}

	return fmt.Errorf("no ingress found with host %s", host)
}

func GenerateRandomChars() string {
	const numBytes = 8
	randBytes := make([]byte, numBytes)
	_, err := rand.Read(randBytes)
	if err != nil {
		panic(err)
	}
	randomString := hex.EncodeToString(randBytes)

	return randomString
}
