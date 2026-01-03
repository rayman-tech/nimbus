package kubernetes

import (
	"context"
	"fmt"

	nimbusEnv "nimbus/internal/env"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func GetNamespace(ctx context.Context, name string, env *nimbusEnv.Env) (*corev1.Namespace, error) {
	return getClient(env).CoreV1().Namespaces().Get(ctx, name, metav1.GetOptions{})
}

func CreateNamespace(ctx context.Context, name string, env *nimbusEnv.Env) error {
	_, err := getClient(env).CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
	}, metav1.CreateOptions{})

	return err
}

func ValidateNamespace(ctx context.Context, name string, env *nimbusEnv.Env) (created bool, err error) {
	ns, err := GetNamespace(ctx, name, env)
	if err == nil && ns != nil {
		return false, nil
	}
	if !errors.IsNotFound(err) {
		return false, fmt.Errorf("getting namespace: %w", err)
	}
	env.Logger.WarnContext(ctx, "namespace does not exist - attempting to create it")

	err = CreateNamespace(ctx, name, env)
	if err != nil {
		return false, fmt.Errorf("creating namespace: %w", err)
	}

	return true, nil
}

func DeleteNamespace(ctx context.Context, name string, env *nimbusEnv.Env) error {
	err := getClient(env).CoreV1().Namespaces().Delete(ctx, name, metav1.DeleteOptions{})
	if err != nil {
		return err
	}
	return nil
}
