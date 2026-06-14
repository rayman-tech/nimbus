package kubernetes

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"nimbus/internal/config"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

var client *kubernetes.Clientset

func InitClient(cfg *config.Config) error {
	var restConfig *rest.Config
	var err error
	if cfg.Environment == "production" {
		slog.Debug("Using in-cluster kubeconfig")
		restConfig, err = rest.InClusterConfig()
		if err != nil {
			return fmt.Errorf("loading in-cluster config: %w", err)
		}
	} else {
		slog.Debug("Using local kubeconfig")
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("getting home directory: %w", err)
		}
		kubeconfig := filepath.Join(home, ".kube", "config")
		restConfig, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
		if err != nil {
			return fmt.Errorf("loading local kubeconfig: %w", err)
		}
	}

	client, err = kubernetes.NewForConfig(restConfig)
	if err != nil {
		return fmt.Errorf("creating kubernetes client: %w", err)
	}
	return nil
}

func getClient() *kubernetes.Clientset {
	return client
}
