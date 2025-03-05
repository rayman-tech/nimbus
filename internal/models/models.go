package models

import (
	corev1 "k8s.io/api/core/v1"
)

type Config struct {
	App      string    `yaml:"app"`
	Services []Service `yaml:"services"`
}

type Service struct {
	Name     string          `yaml:"name"`
	Image    string          `yaml:"image,omitempty"`
	Replicas int32           `yaml:"replicas,omitempty"`
	Network  Network         `yaml:"network,omitempty"`
	Env      []corev1.EnvVar `yaml:"env,omitempty"`
	Volumes  []Volume        `yaml:"volumes,omitempty"`
	Template string          `yaml:"template,omitempty"`
	Version  string          `yaml:"version,omitempty"`
	Configs  []ConfigEntry   `yaml:"configs,omitempty"`
}

type Network struct {
	Ports []int32 `yaml:"ports"`
}

type Volume struct {
	Name      string `yaml:"name"`
	MountPath string `yaml:"mountPath"`
}

type ConfigEntry struct {
	Path  string `yaml:"path"`
	Value string `yaml:"value"`
}
