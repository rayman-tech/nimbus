package kubernetes

import (
	nimbusEnv "nimbus/internal/env"

	"log"
	"os"
	"path/filepath"
	"sync"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

var client *kubernetes.Clientset
var once sync.Once

func getClient(env *nimbusEnv.Env) *kubernetes.Clientset {
	once.Do(func() {
		var config *rest.Config
		var err error
		if os.Getenv("PRODUCTION") == "production" {
			env.Debug("Using in-cluster kubeconfig")
			config, err = rest.InClusterConfig()
			if err != nil {
				log.Fatalf("Failed to load in-cluster config: %v", err)
			}
		} else {
			env.Debug("Using local kubeconfig")
			kubeconfig := filepath.Join(os.Getenv("HOME"), ".kube", "config")
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
