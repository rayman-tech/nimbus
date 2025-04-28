package kubernetes

import (
	"nimbus/internal/database"
	nimbusEnv "nimbus/internal/env"
	"nimbus/internal/models"

	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"
	"time"

	"k8s.io/apimachinery/pkg/api/errors"

	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func GenerateIngressSpec(namespace string, service *models.Service, existingService *database.Service, env *nimbusEnv.Env) (*networkingv1.Ingress, error) {
	switch service.Template {
	case "http":
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
												Number: 80,
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
		if existingService != nil && existingService.Ingress.Valid {
			spec.Rules[0].Host = existingService.Ingress.String
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

	default:
		return nil, nil
	}
}

func CreateIngress(namespace string, ingress *networkingv1.Ingress, env *nimbusEnv.Env) (*networkingv1.Ingress, error) {
	_, err := getClient(env).NetworkingV1().Ingresses(namespace).Create(context.Background(), ingress, metav1.CreateOptions{})
	if err != nil {
		if errors.IsAlreadyExists(err) {
			env.LogAttrs(context.Background(), slog.LevelDebug, "Ingress already exists", slog.String("name", ingress.Name))
			return ingress, nil
		}
		return nil, err
	}
	env.LogAttrs(context.Background(), slog.LevelDebug, "Ingress created", slog.String("name", ingress.Name))
	return ingress, nil
}

func DeleteIngress(namespace, name string, env *nimbusEnv.Env) error {
	err := getClient(env).NetworkingV1().Ingresses(namespace).Delete(context.Background(), name, metav1.DeleteOptions{})
	if err != nil {
		return err
	}
	env.LogAttrs(context.Background(), slog.LevelDebug, "Ingress deleted", slog.String("name", name))
	return nil
}

func GenerateRandomChars() string {
	randBytes := make([]byte, 8)
	_, err := rand.Read(randBytes)
	if err != nil {
		panic(err)
	}
	randomString := hex.EncodeToString(randBytes)

	return randomString
}
