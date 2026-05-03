package config

import (
	"encoding"
	"fmt"
	"strings"

	"nimbus/internal/database"

	"github.com/google/uuid"
	corev1 "k8s.io/api/core/v1"
)

var (
	_ encoding.TextMarshaler   = (*Runtime)(nil)
	_ encoding.TextUnmarshaler = (*Runtime)(nil)
)

type Runtime string

const (
	RuntimeDocker     = "docker"
	RuntimeKubernetes = "kubernetes"
)

func (r Runtime) MarshalText() ([]byte, error) {
	switch r {
	case RuntimeDocker, RuntimeKubernetes:
		return []byte(r), nil
	case "":
		return []byte(RuntimeKubernetes), nil
	default:
		return nil, fmt.Errorf("invalid runtime %q", r)
	}
}

func (r *Runtime) UnmarshalText(text []byte) error {
	raw := strings.TrimSpace(string(text))
	if raw == "" {
		*r = RuntimeKubernetes
		return nil
	}

	switch strings.ToLower(raw) {
	case "docker":
		*r = RuntimeDocker
	case "kubernetes":
		*r = RuntimeKubernetes
	default:
		return fmt.Errorf(
			"invalid runtime %q (expected one of: unknown, docker, kubernetes)",
			raw,
		)
	}
	return nil
}

type Nimbus struct {
	AppName             string    `yaml:"app"`
	RunTime             Runtime   `yaml:"runtime"` // "kubernetes" (default) || "docker"
	AllowBranchPreviews *bool     `yaml:"allowBranchPreviews,omitempty"`
	Services            []Service `yaml:"services"`
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
	Arch         string          `yaml:"arch,omitempty"`
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
	ProjectConfig    Nimbus
	FileContent      []byte
	ExistingServices []database.KubernetesService
}
