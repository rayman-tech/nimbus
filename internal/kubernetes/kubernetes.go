package kubernetes

import (
	"context"
	"log"
	nimbusEnv "nimbus/internal/env"
	"path/filepath"
	"sync"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var client *kubernetes.Clientset
var once sync.Once

func getClient(env *nimbusEnv.Env) *kubernetes.Clientset {
	once.Do(func() {
		var config *rest.Config
		var err error
		if env.Getenv("PRODUCTION") == "production" {
			env.Debug("Using in-cluster kubeconfig")
			config, err = rest.InClusterConfig()
			if err != nil {
				log.Fatalf("Failed to load in-cluster config: %v", err)
			}
		} else {
			env.Debug("Using local kubeconfig")
			kubeconfig := filepath.Join(env.Getenv("HOME"), ".kube", "config")
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

func GetNamespace(name string, env *nimbusEnv.Env) (*corev1.Namespace, error) {
	return getClient(env).CoreV1().Namespaces().Get(context.TODO(), name, metav1.GetOptions{})
}

func CreateNamespace(name string, env *nimbusEnv.Env) error {
	_, err := getClient(env).CoreV1().Namespaces().Create(context.TODO(), &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
	}, metav1.CreateOptions{})

	return err
}
