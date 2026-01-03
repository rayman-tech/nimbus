package kubernetes

import (
	"log"
	"os"
	"path/filepath"
	"sync"

	"nimbus/internal/env"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

var (
	client *kubernetes.Clientset
	once   sync.Once
)

func getClient(env *env.Env) *kubernetes.Clientset {
	once.Do(func() {
		var config *rest.Config
		var err error
		if env.Config.Environment == "production" {
			env.Logger.Debug("Using in-cluster kubeconfig")
			config, err = rest.InClusterConfig()
			if err != nil {
				log.Fatalf("Failed to load in-cluster config: %v", err)
			}
		} else {
			env.Logger.Debug("Using local kubeconfig")
			home, err := os.UserHomeDir()
			if err != nil {
				log.Fatalf("Failed to get home directory: %v", err)
			}
			kubeconfig := filepath.Join(home, ".kube", "config")
			config, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
			if err != nil {
				log.Fatalf("Failed to load local config: %v", err)
			}
		}

		client, err = kubernetes.NewForConfig(config)
		if err != nil {
			log.Fatalf("Failed to create Kubernetes client: %v", err)
		}
	})

	return client
}
