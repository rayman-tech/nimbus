package kubernetes

import (
	"context"
	"testing"

	"nimbus/internal/config"
	"nimbus/internal/database"
	nimbusEnv "nimbus/internal/env"
	"nimbus/internal/models"

	"github.com/google/uuid"
	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/client-go/kubernetes/fake"
)

// stubQuerier embeds the Querier interface so it satisfies the type while only
// implementing the method GenerateDeploymentSpec exercises. Any other call
// would panic on the nil embedded interface, which is the intent for this test.
type stubQuerier struct {
	database.Querier
	identifier uuid.UUID
}

func (s stubQuerier) GetVolumeIdentifier(
	context.Context, database.GetVolumeIdentifierParams,
) (uuid.UUID, error) {
	return s.identifier, nil
}

func TestGenerateDeploymentSpecStrategy(t *testing.T) {
	t.Run("stateless service keeps default rolling update", func(t *testing.T) {
		req := &models.DeployRequest{Namespace: "ns"}
		service := &models.Service{Name: "svc", Image: "img"}

		dep, err := GenerateDeploymentSpec(context.Background(), req, service, &nimbusEnv.Env{})
		if err != nil {
			t.Fatalf("GenerateDeploymentSpec() error = %v", err)
		}

		// An empty strategy type leaves Kubernetes to apply its RollingUpdate default.
		if got := dep.Spec.Strategy.Type; got != "" {
			t.Fatalf("strategy type = %q, want empty (default RollingUpdate)", got)
		}
	})

	t.Run("volume-backed service uses recreate", func(t *testing.T) {
		// Restore the package-level client after swapping in a fake.
		prevClient := client
		client = fake.NewSimpleClientset()
		defer func() { client = prevClient }()

		env := &nimbusEnv.Env{
			Database: stubQuerier{identifier: uuid.New()},
			Config:   &config.Config{},
		}
		req := &models.DeployRequest{Namespace: "ns"}
		service := &models.Service{
			Name:  "db",
			Image: "img",
			Volumes: []models.Volume{
				{Name: "data", MountPath: "/var/lib/data"},
			},
		}

		dep, err := GenerateDeploymentSpec(context.Background(), req, service, env)
		if err != nil {
			t.Fatalf("GenerateDeploymentSpec() error = %v", err)
		}

		if got := dep.Spec.Strategy.Type; got != appsv1.RecreateDeploymentStrategyType {
			t.Fatalf("strategy type = %q, want %q", got, appsv1.RecreateDeploymentStrategyType)
		}
	})
}

func TestGenerateDeploymentSpecMonitoringAnnotations(t *testing.T) {
	tests := []struct {
		name       string
		monitoring *models.Monitoring
		wantScrape bool
		wantPort   string
		wantPath   string
	}{
		{
			name:       "no monitoring block",
			monitoring: nil,
			wantScrape: false,
		},
		{
			name:       "port only defaults path",
			monitoring: &models.Monitoring{Port: 8080},
			wantScrape: true,
			wantPort:   "8080",
			wantPath:   "/metrics",
		},
		{
			name:       "explicit path",
			monitoring: &models.Monitoring{Port: 9090, Path: "/prom"},
			wantScrape: true,
			wantPort:   "9090",
			wantPath:   "/prom",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := &models.DeployRequest{Namespace: "ns"}
			service := &models.Service{Name: "svc", Image: "img", Monitoring: tt.monitoring}

			dep, err := GenerateDeploymentSpec(context.Background(), req, service, &nimbusEnv.Env{})
			if err != nil {
				t.Fatalf("GenerateDeploymentSpec() error = %v", err)
			}

			annotations := dep.Spec.Template.ObjectMeta.Annotations
			_, gotScrape := annotations["prometheus.io/scrape"]
			if gotScrape != tt.wantScrape {
				t.Fatalf("prometheus.io/scrape present = %v, want %v", gotScrape, tt.wantScrape)
			}
			if !tt.wantScrape {
				return
			}
			if got := annotations["prometheus.io/scrape"]; got != "true" {
				t.Errorf("prometheus.io/scrape = %q, want %q", got, "true")
			}
			if got := annotations["prometheus.io/port"]; got != tt.wantPort {
				t.Errorf("prometheus.io/port = %q, want %q", got, tt.wantPort)
			}
			if got := annotations["prometheus.io/path"]; got != tt.wantPath {
				t.Errorf("prometheus.io/path = %q, want %q", got, tt.wantPath)
			}
		})
	}
}
