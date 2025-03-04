package services

import (
	"context"
	"log"
	"sync"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var client *kubernetes.Clientset
var once sync.Once

func getClient() *kubernetes.Clientset {
	once.Do(func() {
		config, err := rest.InClusterConfig()
		if err != nil {
			log.Fatalf("Failed to load in-cluster config: %v", err)
		}

		client, err = kubernetes.NewForConfig(config)
		if err != nil {
			log.Fatalf("Failed to create Kubernetes client: %v", err)
		}
	})

	return client
}

func GetNamespace(name string) (*corev1.Namespace, error) {
	return getClient().CoreV1().Namespaces().Get(context.TODO(), name, metav1.GetOptions{})
}

func CreateNamespace(name string) error {
	_, err := getClient().CoreV1().Namespaces().Create(context.TODO(), &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
	}, metav1.CreateOptions{})

	return err
}

func CreateDeployment(namespace string, deployment *appsv1.Deployment) error {
	_, err := getClient().AppsV1().Deployments(namespace).Create(context.TODO(), deployment, metav1.CreateOptions{})

	return err
}
