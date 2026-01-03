package kubernetes

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"nimbus/internal/database"
	nimbusEnv "nimbus/internal/env"
	"nimbus/internal/models"

	"k8s.io/apimachinery/pkg/api/errors"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	intstr "k8s.io/apimachinery/pkg/util/intstr"
)

const (
	defaultHTTPPort = 80
)

func GenerateServiceSpec(namespace string,
	newService *models.Service, oldService *database.Service,
) (*corev1.Service, error) {
	spec := corev1.ServiceSpec{
		Selector: map[string]string{
			"app": newService.Name,
		},
		Ports: []corev1.ServicePort{},
		Type:  corev1.ServiceTypeClusterIP,
	}

	nodePortEnabled := newService.Public && newService.Template != "http"

	switch newService.Template {
	case "postgres":
		if nodePortEnabled {
			spec.Type = corev1.ServiceTypeNodePort
		}
		spec.Ports = append(spec.Ports, corev1.ServicePort{
			Name: "postgres",
			Port: defaultPostgresPort,
		})
		if nodePortEnabled && oldService != nil && len(oldService.NodePorts) > 0 {
			spec.Ports[0].NodePort = oldService.NodePorts[0]
		}

	case "redis":
		if nodePortEnabled {
			spec.Type = corev1.ServiceTypeNodePort
		}
		spec.Ports = append(spec.Ports, corev1.ServicePort{
			Name: "redis",
			Port: defaultRedisPort,
		})
		if nodePortEnabled && oldService != nil && len(oldService.NodePorts) > 0 {
			spec.Ports[0].NodePort = oldService.NodePorts[0]
		}

	default:
		if nodePortEnabled {
			spec.Type = corev1.ServiceTypeNodePort
		}

		for idx, port := range newService.Network.Ports {
			if nodePortEnabled && oldService != nil && len(oldService.NodePorts) > idx {
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
					Port:       defaultHTTPPort,
					TargetPort: intstr.FromInt(int(port)),
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

func CreateService(ctx context.Context, namespace string, service *corev1.Service, env *nimbusEnv.Env) (*corev1.Service, error) {
	client := getClient(env).CoreV1().Services(namespace)

	existing, err := client.Get(ctx, service.Name, metav1.GetOptions{})
	if errors.IsNotFound(err) {
		env.Logger.WarnContext(ctx, "service not found - creating service", slog.String("service", service.Name))
		service, err := client.Create(context.Background(), service, metav1.CreateOptions{})
		if err != nil {
			return nil, fmt.Errorf("creating service: %w", err)
		}
		return service, nil
	} else if err != nil {
		return nil, fmt.Errorf("failed to get service: %w", err)
	}

	existing.Spec = service.Spec

	if existing.Annotations == nil {
		existing.Annotations = make(map[string]string)
	}
	existing.Annotations["updated"] = time.Now().Format(time.RFC3339)
	updated, err := client.Update(ctx, existing, metav1.UpdateOptions{})
	if err != nil {
		return nil, fmt.Errorf("updating deployment: %w", err)
	}

	return updated, nil
}

func DeleteService(ctx context.Context, namespace, name string, env *nimbusEnv.Env) error {
	client := getClient(env).CoreV1().Services(namespace)

	err := client.Delete(ctx, name, metav1.DeleteOptions{})
	if err != nil {
		return fmt.Errorf("failed to delete service: %w", err)
	}

	return nil
}
