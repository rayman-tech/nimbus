package kubernetes

import (
	nimbusEnv "nimbus/internal/env"

	"context"
	"fmt"
	"log/slog"
	"sort"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func GetSecret(namespace string, env *nimbusEnv.Env) (*corev1.Secret, error) {
	client := getClient(env).CoreV1().Secrets(namespace)
	secret, err := client.Get(context.Background(), fmt.Sprintf("%s-env", namespace), metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	return secret, nil
}

func GetSecretValues(namespace string, env *nimbusEnv.Env) (map[string]string, error) {
	secret, err := GetSecret(namespace, env)
	if err != nil {
		if errors.IsNotFound(err) {
			return map[string]string{}, nil
		}
		return nil, err
	}
	out := make(map[string]string, len(secret.Data))
	for k, v := range secret.Data {
		out[k] = string(v)
	}
	return out, nil
}

func ListSecretNames(namespace string, env *nimbusEnv.Env) ([]string, error) {
	secret, err := GetSecret(namespace, env)
	if err != nil {
		if errors.IsNotFound(err) {
			return []string{}, nil
		}
		return nil, err
	}
	keys := make([]string, 0, len(secret.Data))
	for k := range secret.Data {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys, nil
}

func UpdateSecret(namespace, name string, data map[string]string, env *nimbusEnv.Env) error {
	_, err := ValidateNamespace(namespace, env, context.Background())
	if err != nil {
		return fmt.Errorf("failed to validate namespace %s: %w", namespace, err)
	}

	client := getClient(env).CoreV1().Secrets(namespace)
	var secret *corev1.Secret
	secret, err = client.Get(context.Background(), name, metav1.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			_, err = client.Create(context.Background(), &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name,
					Namespace: namespace,
				},
				StringData: data,
				Type:       corev1.SecretTypeOpaque,
			}, metav1.CreateOptions{})
			return err
		}
		return err
	}

	// replace the existing data entirely so removed keys are deleted
	newData := make(map[string][]byte)
	for k, v := range data {
		newData[k] = []byte(v)
	}

	secret.Data = newData
	secret.StringData = nil
	secret.Type = corev1.SecretTypeOpaque

	_, err = client.Update(context.Background(), secret, metav1.UpdateOptions{})
	return err
}
