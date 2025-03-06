package kubernetes

import (
	"nimbus/internal/database"
	"nimbus/internal/models"

	"context"
	"fmt"
	"log"
	"time"

	"k8s.io/apimachinery/pkg/api/errors"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func GenerateServiceSpec(namespace string, service *models.Service, existingService *database.Service) (*corev1.Service, error) {
	spec := corev1.ServiceSpec{
		Selector: map[string]string{
			"app": service.Name,
		},
		Ports: []corev1.ServicePort{},
	}

	switch service.Template {
	case "postgres":
		spec.Type = corev1.ServiceTypeNodePort
		spec.Ports = append(spec.Ports, corev1.ServicePort{
			Name: "postgres",
			Port: 5432,
		})
		if existingService != nil && len(existingService.NodePorts) > 0 && existingService.NodePorts[0] == 5432 {
			spec.Ports[0].NodePort = existingService.NodePorts[0]
		}

	case "redis":
		spec.Type = corev1.ServiceTypeNodePort
		spec.Ports = append(spec.Ports, corev1.ServicePort{
			Name: "redis",
			Port: 6379,
		})
		if existingService != nil && len(existingService.NodePorts) > 0 && existingService.NodePorts[0] == 6379 {
			spec.Ports[0].NodePort = existingService.NodePorts[0]
		}

	default:
		if service.Template != "http" {
			spec.Type = corev1.ServiceTypeNodePort
		}

		for _, port := range service.Network.Ports {
			if existingService != nil && len(existingService.NodePorts) > 0 && existingService.NodePorts[0] == port {
				spec.Ports = append(spec.Ports, corev1.ServicePort{
					Name:     fmt.Sprintf("port-%d", port),
					Port:     port,
					NodePort: existingService.NodePorts[0],
				})
			} else {
				spec.Ports = append(spec.Ports, corev1.ServicePort{
					Name: fmt.Sprintf("port-%d", port),
					Port: port,
				})
			}
		}
	}

	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      service.Name,
			Namespace: namespace,
		},
		Spec: spec,
	}, nil
}

func CreateService(namespace string, service *corev1.Service) (*corev1.Service, error) {
	client := getClient().CoreV1().Services(namespace)

	existing, err := client.Get(context.TODO(), service.Name, metav1.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			log.Printf("Service %s not found, creating new one.", service.Name)
			return client.Create(context.TODO(), service, metav1.CreateOptions{})
		}
		return nil, fmt.Errorf("failed to get service: %w", err)
	}

	existing.Spec = service.Spec

	if existing.Annotations == nil {
		existing.Annotations = make(map[string]string)
	}
	existing.Annotations["updated"] = time.Now().Format(time.RFC3339)

	log.Printf("Updating service %s...", service.Name)
	updated, err := client.Update(context.TODO(), existing, metav1.UpdateOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to update deployment: %w", err)
	}

	return updated, nil
}

func DeleteService(namespace, name string) error {
	client := getClient().CoreV1().Services(namespace)

	err := client.Delete(context.TODO(), name, metav1.DeleteOptions{})
	if err != nil {
		return fmt.Errorf("failed to delete service: %w", err)
	}

	return nil
}
