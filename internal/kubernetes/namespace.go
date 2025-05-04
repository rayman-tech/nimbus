package kubernetes

import (
	"context"
	"log/slog"
	nimbusEnv "nimbus/internal/env"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

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

func EnsureNamespace(name string, env *nimbusEnv.Env, ctx context.Context) error {
	ns, err := GetNamespace(name, env)
	if err == nil && ns != nil {
		return nil
	}
	env.LogAttrs(
		ctx, slog.LevelError,
		"Error retrieving namespace. Attempting to create it", slog.Any("error", err),
	)

	err = CreateNamespace(name, env)
	if err != nil {
		env.LogAttrs(
			ctx, slog.LevelError,
			"Error creating namespace", slog.Any("error", err),
		)
		return err
	}

	return nil
}
