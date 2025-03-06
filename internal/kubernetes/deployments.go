package kubernetes

import (
	"nimbus/internal/models"

	"context"
	"fmt"
	"log"
	"time"

	"k8s.io/apimachinery/pkg/api/errors"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func GenerateDeploymentSpec(namespace string, service *models.Service) (*appsv1.Deployment, error) {
	var defaultReplicas int32 = 1
	spec := appsv1.DeploymentSpec{
		Replicas: &defaultReplicas,
		Selector: &metav1.LabelSelector{
			MatchLabels: map[string]string{
				"app": service.Name,
			},
		},
		Template: corev1.PodTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{
				Annotations: map[string]string{
					"kubectl.kubernetes.io/restartedAt": time.Now().Format(time.RFC3339),
				},
				Labels: map[string]string{
					"app": service.Name,
				},
				Namespace: namespace,
			},
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name:  service.Name,
						Image: service.Image,
						Env:   service.Env,
					},
				},
			},
		},
	}

	switch service.Template {
	case "postgres":
		// TODO: check existing PV and PVC, if not found then create them
		if service.Version == "" {
			service.Version = "13"
		}
		spec.Template.Spec.Containers[0].Image = fmt.Sprintf("postgres:%s", service.Version)
		spec.Template.Spec.Containers[0].Env = []corev1.EnvVar{
			{
				Name:  "POSTGRES_USER",
				Value: "postgres",
			},
			{
				Name:  "POSTGRES_PASSWORD",
				Value: "postgres",
			},
			{
				Name:  "POSTGRES_DB",
				Value: "postgres",
			},
		}
		spec.Template.Spec.Containers[0].Ports = []corev1.ContainerPort{
			{
				Name:          "postgres",
				ContainerPort: 5432,
			},
		}
		spec.Template.Spec.Containers[0].VolumeMounts = []corev1.VolumeMount{
			{
				Name:      "postgres-storage",
				MountPath: "/var/lib/postgresql/data",
			},
		}
		spec.Template.Spec.Volumes = []corev1.Volume{
			{
				Name: "postgres-storage",
				VolumeSource: corev1.VolumeSource{
					// TODO: use persistent volume
					EmptyDir: &corev1.EmptyDirVolumeSource{},
				},
			},
		}

	case "redis":
		if service.Version == "" {
			service.Version = "6"
		}
		spec.Template.Spec.Containers[0].Image = fmt.Sprintf("redis:%s", service.Version)
		spec.Template.Spec.Containers[0].Ports = []corev1.ContainerPort{
			{
				Name:          "redis",
				ContainerPort: 6379,
			},
		}
		spec.Template.Spec.Containers[0].VolumeMounts = []corev1.VolumeMount{
			{
				Name:      "redis-storage",
				MountPath: "/data",
			},
		}
		spec.Template.Spec.Volumes = []corev1.Volume{
			{
				Name: "redis-storage",
				VolumeSource: corev1.VolumeSource{
					EmptyDir: &corev1.EmptyDirVolumeSource{},
				},
			},
		}

	default:
		for idx, port := range service.Network.Ports {
			spec.Template.Spec.Containers[0].Ports = append(spec.Template.Spec.Containers[0].Ports, corev1.ContainerPort{
				Name:          fmt.Sprintf("port-%d", idx),
				ContainerPort: port,
			})
		}
	}

	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      service.Name,
			Namespace: namespace,
		},
		Spec: spec,
	}, nil
}

func CreateDeployment(namespace string, deployment *appsv1.Deployment) (*appsv1.Deployment, error) {
	client := getClient().AppsV1().Deployments(namespace)

	existing, err := client.Get(context.TODO(), deployment.Name, metav1.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			log.Printf("Deployment %s not found, creating new one.", deployment.Name)
			return client.Create(context.TODO(), deployment, metav1.CreateOptions{})
		}
		return nil, fmt.Errorf("failed to get deployment: %w", err)
	}

	existing.Spec = deployment.Spec

	if existing.Spec.Template.Annotations == nil {
		existing.Spec.Template.Annotations = make(map[string]string)
	}
	existing.Spec.Template.Annotations["kubectl.kubernetes.io/restartedAt"] = time.Now().Format(time.RFC3339)

	log.Printf("Updating deployment %s...", deployment.Name)
	updated, err := client.Update(context.TODO(), existing, metav1.UpdateOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to update deployment: %w", err)
	}

	return updated, nil
}

func DeleteDeployment(namespace, name string) error {
	client := getClient().AppsV1().Deployments(namespace)

	err := client.Delete(context.TODO(), name, metav1.DeleteOptions{})
	if err != nil {
		return fmt.Errorf("failed to delete deployment: %w", err)
	}

	return nil
}
