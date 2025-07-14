package kubernetes

import (
	nimbusEnv "nimbus/internal/env"

	"context"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func GetPods(namespace, serviceName string, env *nimbusEnv.Env) ([]corev1.Pod, error) {
	pods, err := getClient(env).CoreV1().Pods(namespace).List(context.Background(), metav1.ListOptions{
		LabelSelector: "app=" + serviceName,
	})
	if err != nil {
		return nil, err
	}
	return pods.Items, nil
}
