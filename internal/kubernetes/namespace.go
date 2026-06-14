package kubernetes

import (
	"context"
	"fmt"
	"log/slog"

	"nimbus/internal/config"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func GetNamespace(ctx context.Context, name string, cfg *config.Config) (*corev1.Namespace, error) {
	return getClient(cfg).CoreV1().Namespaces().Get(ctx, name, metav1.GetOptions{})
}

func CreateNamespace(ctx context.Context, name string, cfg *config.Config) error {
	_, err := getClient(cfg).CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
	}, metav1.CreateOptions{})

	return err
}

func ValidateNamespace(ctx context.Context, name string, cfg *config.Config) (created bool, err error) {
	ns, err := GetNamespace(ctx, name, cfg)
	if err == nil && ns != nil {
		return false, nil
	}
	if !errors.IsNotFound(err) {
		return false, fmt.Errorf("getting namespace: %w", err)
	}
	slog.WarnContext(ctx, "namespace does not exist - attempting to create it")

	err = CreateNamespace(ctx, name, cfg)
	if err != nil {
		return false, fmt.Errorf("creating namespace: %w", err)
	}

	return true, nil
}

func DeleteNamespace(ctx context.Context, name string, cfg *config.Config) error {
	err := getClient(cfg).CoreV1().Namespaces().Delete(ctx, name, metav1.DeleteOptions{})
	if err != nil && !errors.IsNotFound(err) {
		return err
	}
	return nil
}
