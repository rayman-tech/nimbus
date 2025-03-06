package services

import (
	"nimbus/internal/database"
	"nimbus/internal/models"

	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"k8s.io/apimachinery/pkg/api/errors"

	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func GenerateIngressSpec(namespace string, service *models.Service, existingService *database.Service) (*networkingv1.Ingress, error) {
	switch service.Template {
	case "postgres":
		return nil, nil
	case "redis":
		return nil, nil
	default:
		spec := networkingv1.IngressSpec{
			Rules: []networkingv1.IngressRule{
				{
					Host: fmt.Sprintf("%s.%s", GenerateRandomChars(), os.Getenv("DOMAIN")),
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
		}
		if existingService != nil && existingService.Ingress.String != "" {
			spec.Rules[0].Host = existingService.Ingress.String
		} else {
			if existingService == nil {
				database.GetQueries().CreateService(context.TODO(), database.CreateServiceParams{
					Name:        service.Name,
					ProjectName: namespace,
					Ingress:     pgtype.Text{String: spec.Rules[0].Host},
				})
			} else {
				database.GetQueries().SetServiceIngress(context.TODO(), database.SetServiceIngressParams{
					Name:        service.Name,
					ProjectName: namespace,
					Ingress:     pgtype.Text{String: spec.Rules[0].Host},
				})
			}
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
}

func CreateIngress(namespace string, ingress *networkingv1.Ingress) (*networkingv1.Ingress, error) {
	_, err := getClient().NetworkingV1().Ingresses(namespace).Create(context.Background(), ingress, metav1.CreateOptions{})
	if err != nil {
		if errors.IsAlreadyExists(err) {
			log.Printf("Ingress already exists: %s\n", ingress.Name)
			return ingress, nil
		}
		return nil, err
	}
	log.Printf("Created ingress: %s\n", ingress.Name)
	return ingress, nil
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
