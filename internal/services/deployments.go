package services

import (
	"nimbus/internal/models"

	"context"
	"fmt"

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

	case "default":
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

func CreateDeployment(namespace string, deployment *appsv1.Deployment) error {
	_, err := getClient().AppsV1().Deployments(namespace).Create(context.TODO(), deployment, metav1.CreateOptions{})

	return err
}
