package models

import (
	"nimbus/internal/database"

	"github.com/google/uuid"

	corev1 "k8s.io/api/core/v1"
)

type Config struct {
	AppName  string    `yaml:"app"`
	Services []Service `yaml:"services"`
}

type Service struct {
	Name         string          `yaml:"name"`
	Image        string          `yaml:"image,omitempty"`
	Replicas     int32           `yaml:"replicas,omitempty"`
	Network      Network         `yaml:"network,omitempty"`
	Env          []corev1.EnvVar `yaml:"env,omitempty"`
	EnvOverrides []Override      `yaml:"envOverrides,omitempty"`
	Volumes      []Volume        `yaml:"volumes,omitempty"`
	Public       bool            `yaml:"public,omitempty"`
	Template     string          `yaml:"template,omitempty"`
	Version      string          `yaml:"version,omitempty"`
	Configs      []ConfigEntry   `yaml:"configs,omitempty"`
	Command      []string        `yaml:"command,omitempty"`
	Args         []string        `yaml:"args,omitempty"`
}

type Network struct {
	Ports []int32 `yaml:"ports"`
}

type Override struct {
	Name    string `yaml:"name"`
	Service string `yaml:"service"`
	Field   string `yaml:"field"` // "internal-host" || "ingress-host" || "port"
}

type Volume struct {
	Name      string `yaml:"name"`
	MountPath string `yaml:"mountPath"`
	Size      int32  `yaml:"size,omitempty"`
}

type ConfigEntry struct {
	Path  string `yaml:"path"`
	Value string `yaml:"value"`
}

type DeployRequest struct {
	Namespace        string
	ProjectID        uuid.UUID
	BranchName       string
	ProjectConfig    Config
	FileContent      []byte
	ExistingServices []database.Service
}
