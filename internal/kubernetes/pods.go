package kubernetes

import (
	"context"
	"fmt"
	"io"

	"nimbus/internal/config"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func GetPods(ctx context.Context, namespace, serviceName string, cfg *config.Config) ([]corev1.Pod, error) {
	pods, err := getClient(cfg).CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: "app=" + serviceName,
	})
	if err != nil {
		return nil, err
	}
	return pods.Items, nil
}

// StreamPodLogs streams logs for a specific pod within a namespace. The caller
// should close the returned ReadCloser when finished.
func StreamPodLogs(ctx context.Context, namespace, podName string, cfg *config.Config) (io.ReadCloser, error) {
	req := getClient(cfg).CoreV1().Pods(namespace).GetLogs(podName, &corev1.PodLogOptions{Follow: true})
	stream, err := req.Stream(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to stream logs: %w", err)
	}
	return stream, nil
}

// StreamServiceLogs retrieves the first pod for the given service and streams
// its logs. If no pods are found an error is returned.
func StreamServiceLogs(ctx context.Context, namespace, serviceName string, cfg *config.Config) (io.ReadCloser, error) {
	pods, err := GetPods(ctx, namespace, serviceName, cfg)
	if err != nil {
		return nil, err
	}
	if len(pods) == 0 {
		return nil, fmt.Errorf("no pods found for service %s", serviceName)
	}

	var lines int64 = 20
	req := getClient(cfg).CoreV1().Pods(namespace).GetLogs(
		pods[0].Name, &corev1.PodLogOptions{Follow: true, TailLines: &lines})
	stream, err := req.Stream(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to stream logs: %w", err)
	}
	return stream, nil
}

// GetPodLogs retrieves the full logs for a given pod.
func GetPodLogs(ctx context.Context, namespace, podName string, cfg *config.Config) ([]byte, error) {
	req := getClient(cfg).CoreV1().Pods(namespace).GetLogs(podName, &corev1.PodLogOptions{})
	return req.Do(ctx).Raw()
}

// GetPodLogsTail retrieves the last n lines of logs for a given pod.
func GetPodLogsTail(ctx context.Context, namespace, podName string, lines int64, cfg *config.Config) ([]byte, error) {
	req := getClient(cfg).CoreV1().Pods(namespace).GetLogs(podName, &corev1.PodLogOptions{TailLines: &lines})
	return req.Do(ctx).Raw()
}

// GetServiceLogs retrieves the full logs for the first pod of the service.
func GetServiceLogs(ctx context.Context, namespace, serviceName string, cfg *config.Config) ([]byte, error) {
	pods, err := GetPods(ctx, namespace, serviceName, cfg)
	if err != nil {
		return nil, err
	}
	if len(pods) == 0 {
		return nil, fmt.Errorf("no pods found for service %s", serviceName)
	}
	return GetPodLogs(ctx, namespace, pods[0].Name, cfg)
}

// GetServiceLogsTail retrieves the last n lines of logs for the first pod of the service.
func GetServiceLogsTail(
	ctx context.Context, namespace, serviceName string, lines int64, cfg *config.Config,
) ([]byte, error) {
	pods, err := GetPods(ctx, namespace, serviceName, cfg)
	if err != nil {
		return nil, err
	}
	if len(pods) == 0 {
		return nil, fmt.Errorf("no pods found for service %s", serviceName)
	}
	return GetPodLogsTail(ctx, namespace, pods[0].Name, lines, cfg)
}
