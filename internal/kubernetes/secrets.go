package kubernetes

import (
	nimbusEnv "nimbus/internal/env"

	"context"
	"sort"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const ProjectSecretName = "project-secrets"

func GetSecret(namespace, name string, env *nimbusEnv.Env) (*corev1.Secret, error) {
	client := getClient(env).CoreV1().Secrets(namespace)
	secret, err := client.Get(context.Background(), name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	return secret, nil
}

func GetSecretValues(namespace, name string, env *nimbusEnv.Env) (map[string]string, error) {
	secret, err := GetSecret(namespace, name, env)
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

func ListSecretNames(namespace, name string, env *nimbusEnv.Env) ([]string, error) {
	secret, err := GetSecret(namespace, name, env)
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

func CreateOrUpdateSecret(namespace, name string, data map[string]string, env *nimbusEnv.Env) error {
	client := getClient(env).CoreV1().Secrets(namespace)
	existing, err := client.Get(context.Background(), name, metav1.GetOptions{})
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

	existing.StringData = data
	existing.Type = corev1.SecretTypeOpaque
	_, err = client.Update(context.Background(), existing, metav1.UpdateOptions{})
	return err
}
