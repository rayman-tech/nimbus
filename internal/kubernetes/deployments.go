package kubernetes

import (
	nimbusEnv "nimbus/internal/env"
	"nimbus/internal/models"

	"context"
	"fmt"
	"log"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func GenerateDeploymentSpec(namespace string, service *models.Service, env *nimbusEnv.Env) (*appsv1.Deployment, error) {
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
						Name:         service.Name,
						Image:        service.Image,
						Env:          service.Env,
						VolumeMounts: []corev1.VolumeMount{},
					},
				},
				Volumes: []corev1.Volume{},
			},
		},
	}

	if service.Command != nil {
		spec.Template.Spec.Containers[0].Command = service.Command
	}
	if service.Args != nil {
		spec.Template.Spec.Containers[0].Args = service.Args
	}

	switch service.Template {
	case "postgres":
		if service.Version == "" {
			service.Version = "13"
		}
		if len(service.Volumes) == 0 {
			service.Volumes = []models.Volume{{
				Name:      fmt.Sprintf("%s-psql", service.Name),
				MountPath: "/var/lib/postgresql/data",
			}}
		}

		spec.Template.Spec.Containers[0].Image = fmt.Sprintf("postgres:%s", service.Version)
		if checkEnvironment(service.Env, "POSTGRES_USER") == nil {
			spec.Template.Spec.Containers[0].Env = append(spec.Template.Spec.Containers[0].Env, corev1.EnvVar{
				Name:  "POSTGRES_USER",
				Value: "postgres",
			})
		}
		if checkEnvironment(service.Env, "POSTGRES_PASSWORD") == nil {
			spec.Template.Spec.Containers[0].Env = append(spec.Template.Spec.Containers[0].Env, corev1.EnvVar{
				Name:  "POSTGRES_PASSWORD",
				Value: "postgres",
			})
		}
		if checkEnvironment(service.Env, "POSTGRES_DB") == nil {
			spec.Template.Spec.Containers[0].Env = append(spec.Template.Spec.Containers[0].Env, corev1.EnvVar{
				Name:  "POSTGRES_DB",
				Value: "postgres",
			})
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
		if len(service.Volumes) == 0 {
			service.Volumes = []models.Volume{{
				Name:      fmt.Sprintf("%s-redis", service.Name),
				MountPath: "/data",
			}}
		}

		spec.Template.Spec.Containers[0].Image = fmt.Sprintf("redis:%s", service.Version)
		spec.Template.Spec.Containers[0].Ports = []corev1.ContainerPort{
			{
				Name:          "redis",
				ContainerPort: 6379,
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

	if len(service.Volumes) > 0 {
		volumeMap, err := GetVolumeIdentifiers(namespace, service, env)
		if err != nil {
			return nil, fmt.Errorf("failed to get volume identifiers: %w", err)
		}
		log.Printf("Volume map: %+v", volumeMap)

		for name, volume := range volumeMap {
			spec.Template.Spec.Volumes = append(spec.Template.Spec.Volumes, corev1.Volume{
				Name: name,
				VolumeSource: corev1.VolumeSource{
					PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
						ClaimName: volume.PVC,
					},
				},
			})
			spec.Template.Spec.Containers[0].VolumeMounts = append(spec.Template.Spec.Containers[0].VolumeMounts, corev1.VolumeMount{
				Name:      name,
				MountPath: volume.MountPath,
			})
		}
	}

	if service.Arch != "" {
		spec.Template.Spec.Affinity = &corev1.Affinity{
			NodeAffinity: &corev1.NodeAffinity{
				RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
					NodeSelectorTerms: []corev1.NodeSelectorTerm{
						{
							MatchExpressions: []corev1.NodeSelectorRequirement{
								{
									Key:      "kubernetes.io/arch",
									Operator: corev1.NodeSelectorOpIn,
									Values:   []string{service.Arch},
								},
							},
						},
					},
				},
			},
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

func CreateDeployment(namespace string, deployment *appsv1.Deployment, env *nimbusEnv.Env) (*appsv1.Deployment, error) {
	client := getClient(env).AppsV1().Deployments(namespace)

	existing, err := client.Get(context.Background(), deployment.Name, metav1.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			env.Logger.Debug("Deployment not found, creating new one.", "name", deployment.Name)
			return client.Create(context.Background(), deployment, metav1.CreateOptions{})
		}
		return nil, fmt.Errorf("failed to get deployment: %w", err)
	}

	existing.Spec = deployment.Spec

	if existing.Spec.Template.Annotations == nil {
		existing.Spec.Template.Annotations = make(map[string]string)
	}
	existing.Spec.Template.Annotations["kubectl.kubernetes.io/restartedAt"] = time.Now().Format(time.RFC3339)

	log.Printf("Updating deployment %s...", deployment.Name)
	updated, err := client.Update(context.Background(), existing, metav1.UpdateOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to update deployment: %w", err)
	}

	return updated, nil
}

func DeleteDeployment(namespace, name string, env *nimbusEnv.Env) error {
	client := getClient(env).AppsV1().Deployments(namespace)

	err := client.Delete(context.Background(), name, metav1.DeleteOptions{})
	if err != nil {
		return fmt.Errorf("failed to delete deployment: %w", err)
	}

	return nil
}

func checkEnvironment(vars []corev1.EnvVar, key string) *string {
	for _, v := range vars {
		if v.Name == key {
			return &v.Value
		}
	}
	return nil
}
