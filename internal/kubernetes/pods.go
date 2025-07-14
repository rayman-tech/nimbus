package kubernetes

import (
	nimbusEnv "nimbus/internal/env"

	"context"
	"fmt"
	"io"
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

// StreamPodLogs streams logs for a specific pod within a namespace. The caller
// should close the returned ReadCloser when finished.
func StreamPodLogs(namespace, podName string, env *nimbusEnv.Env) (io.ReadCloser, error) {
	req := getClient(env).CoreV1().Pods(namespace).GetLogs(podName, &corev1.PodLogOptions{Follow: true})
	stream, err := req.Stream(context.Background())
	if err != nil {
		return nil, fmt.Errorf("failed to stream logs: %w", err)
	}
	return stream, nil
}

// StreamServiceLogs retrieves the first pod for the given service and streams
// its logs. If no pods are found an error is returned.
func StreamServiceLogs(namespace, serviceName string, env *nimbusEnv.Env) (io.ReadCloser, error) {
	pods, err := GetPods(namespace, serviceName, env)
	if err != nil {
		return nil, err
	}
	if len(pods) == 0 {
		return nil, fmt.Errorf("no pods found for service %s", serviceName)
	}
	return StreamPodLogs(namespace, pods[0].Name, env)
}
