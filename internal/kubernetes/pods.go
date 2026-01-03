package kubernetes

import (
	"context"
	"fmt"
	"io"

	nimbusEnv "nimbus/internal/env"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func GetPods(ctx context.Context, namespace, serviceName string, env *nimbusEnv.Env) ([]corev1.Pod, error) {
	pods, err := getClient(env).CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
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
func StreamServiceLogs(ctx context.Context, namespace, serviceName string, env *nimbusEnv.Env) (io.ReadCloser, error) {
	pods, err := GetPods(ctx, namespace, serviceName, env)
	if err != nil {
		return nil, err
	}
	if len(pods) == 0 {
		return nil, fmt.Errorf("no pods found for service %s", serviceName)
	}

	var lines int64 = 20
	req := getClient(env).CoreV1().Pods(namespace).GetLogs(
		pods[0].Name, &corev1.PodLogOptions{Follow: true, TailLines: &lines})
	stream, err := req.Stream(context.Background())
	if err != nil {
		return nil, fmt.Errorf("failed to stream logs: %w", err)
	}
	return stream, nil
}

// GetPodLogs retrieves the full logs for a given pod.
func GetPodLogs(namespace, podName string, env *nimbusEnv.Env) ([]byte, error) {
	req := getClient(env).CoreV1().Pods(namespace).GetLogs(podName, &corev1.PodLogOptions{})
	return req.Do(context.Background()).Raw()
}

// GetPodLogsTail retrieves the last n lines of logs for a given pod.
func GetPodLogsTail(ctx context.Context, namespace, podName string, lines int64, env *nimbusEnv.Env) ([]byte, error) {
	req := getClient(env).CoreV1().Pods(namespace).GetLogs(podName, &corev1.PodLogOptions{TailLines: &lines})
	return req.Do(ctx).Raw()
}

// GetServiceLogs retrieves the full logs for the first pod of the service.
func GetServiceLogs(ctx context.Context, namespace, serviceName string, env *nimbusEnv.Env) ([]byte, error) {
	pods, err := GetPods(ctx, namespace, serviceName, env)
	if err != nil {
		return nil, err
	}
	if len(pods) == 0 {
		return nil, fmt.Errorf("no pods found for service %s", serviceName)
	}
	return GetPodLogs(namespace, pods[0].Name, env)
}

// GetServiceLogsTail retrieves the last n lines of logs for the first pod of the service.
func GetServiceLogsTail(
	ctx context.Context, namespace, serviceName string, lines int64, env *nimbusEnv.Env,
) ([]byte, error) {
	pods, err := GetPods(ctx, namespace, serviceName, env)
	if err != nil {
		return nil, err
	}
	if len(pods) == 0 {
		return nil, fmt.Errorf("no pods found for service %s", serviceName)
	}
	return GetPodLogsTail(ctx, namespace, pods[0].Name, lines, env)
}
