package models

import (
	"nimbus/internal/database"

	"github.com/goccy/go-yaml"
	"github.com/google/uuid"

	corev1 "k8s.io/api/core/v1"
)

type Config struct {
	AppName             string    `yaml:"app"`
	AllowBranchPreviews *bool     `yaml:"allowBranchPreviews,omitempty"`
	Services            []Service `yaml:"services"`
}

// Auth selects the cluster's authentication provider for a public HTTP service.
type Auth struct {
	Provider string `yaml:"provider"`
}

// Reject misspelled fields and server overrides instead of silently ignoring them.
func (a *Auth) UnmarshalYAML(data []byte) error {
	type plainAuth Auth
	var decoded plainAuth
	if err := yaml.UnmarshalWithOptions(data, &decoded, yaml.Strict()); err != nil {
		return err
	}
	*a = Auth(decoded)
	return nil
}

type Service struct {
	Auth         *Auth             `yaml:"auth,omitempty"`
	Name         string            `yaml:"name"`
	Image        string            `yaml:"image,omitempty"`
	Replicas     int32             `yaml:"replicas,omitempty"`
	Network      Network           `yaml:"network,omitempty"`
	Env          []corev1.EnvVar   `yaml:"env,omitempty"`
	EnvOverrides []Override        `yaml:"envOverrides,omitempty"`
	Volumes      []Volume          `yaml:"volumes,omitempty"`
	Public       bool              `yaml:"public,omitempty"`
	Ingress      string            `yaml:"ingress,omitempty"`
	Annotations  map[string]string `yaml:"annotations,omitempty"`
	Template     string            `yaml:"template,omitempty"`
	Version      string            `yaml:"version,omitempty"`
	Arch         string            `yaml:"arch,omitempty"`
	Features     []string          `yaml:"features,omitempty"`
	Configs      []ConfigEntry     `yaml:"configs,omitempty"`
	Command      []string          `yaml:"command,omitempty"`
	Args         []string          `yaml:"args,omitempty"`
	Monitoring   *Monitoring       `yaml:"monitoring,omitempty"`
}

// Monitoring configures Prometheus scraping for a service. When present, nimbus
// adds the standard prometheus.io/* annotations to the service's pods so a
// Prometheus instance using the kubernetes-pods scrape job will discover and
// scrape the metrics endpoint. Its presence is the on switch — omit the block
// to disable scraping.
type Monitoring struct {
	Port int32  `yaml:"port"`           // container port exposing metrics; required, must be > 0
	Path string `yaml:"path,omitempty"` // metrics path; defaults to "/metrics"
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
	CommitHash       string
	ProjectConfig    Config
	FileContent      []byte
	ExistingServices []database.Service
}
