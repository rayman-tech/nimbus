package kubernetes

import (
	"nimbus/internal/database"
	nimbusEnv "nimbus/internal/env"
	"nimbus/internal/models"

	"context"
	"fmt"
	"log"
	"time"

	"k8s.io/apimachinery/pkg/api/errors"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	intstr "k8s.io/apimachinery/pkg/util/intstr"
)

func GenerateServiceSpec(namespace string, newService *models.Service, oldService *database.Service) (*corev1.Service, error) {
	spec := corev1.ServiceSpec{
		Selector: map[string]string{
			"app": newService.Name,
		},
		Ports: []corev1.ServicePort{},
	}

	switch newService.Template {
	case "postgres":
		spec.Type = corev1.ServiceTypeNodePort
		spec.Ports = append(spec.Ports, corev1.ServicePort{
			Name: "postgres",
			Port: 5432,
		})
		if oldService != nil && len(oldService.NodePorts) > 0 {
			spec.Ports[0].NodePort = oldService.NodePorts[0]
		}

	case "redis":
		spec.Type = corev1.ServiceTypeNodePort
		spec.Ports = append(spec.Ports, corev1.ServicePort{
			Name: "redis",
			Port: 6379,
		})
		if oldService != nil && len(oldService.NodePorts) > 0 {
			spec.Ports[0].NodePort = oldService.NodePorts[0]
		}

	default:
		if newService.Template != "http" {
			spec.Type = corev1.ServiceTypeNodePort
		}

		for idx, port := range newService.Network.Ports {
			if oldService != nil && len(oldService.NodePorts) > idx {
				spec.Ports = append(spec.Ports, corev1.ServicePort{
					Name:     fmt.Sprintf("port-%d", idx),
					Port:     port,
					NodePort: oldService.NodePorts[idx],
				})
			} else {
				// if we set the type as NodePort (not an HTTP template),
				// then a NodePort will be be randomly selected
				// otherwise, will use this port as ClusterIP
				spec.Ports = append(spec.Ports, corev1.ServicePort{
					Name:       fmt.Sprintf("port-%d", idx),
					Port:       port,
					TargetPort: intstr.FromInt(80),
				})
			}
		}
	}

	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      newService.Name,
			Namespace: namespace,
		},
		Spec: spec,
	}, nil
}

func CreateService(namespace string, service *corev1.Service, env *nimbusEnv.Env) (*corev1.Service, error) {
	client := getClient(env).CoreV1().Services(namespace)

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

func DeleteService(namespace, name string, env *nimbusEnv.Env) error {
	client := getClient(env).CoreV1().Services(namespace)

	err := client.Delete(context.TODO(), name, metav1.DeleteOptions{})
	if err != nil {
		return fmt.Errorf("failed to delete service: %w", err)
	}

	return nil
}
