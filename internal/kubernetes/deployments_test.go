package kubernetes

import (
	"context"
	"testing"

	"nimbus/internal/config"
	"nimbus/internal/database"
	nimbusEnv "nimbus/internal/env"
	"nimbus/internal/imageupdate"
	"nimbus/internal/models"

	"github.com/goccy/go-yaml"
	"github.com/google/uuid"
	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

// stubQuerier embeds the Querier interface so it satisfies the type while only
// implementing the method GenerateDeploymentSpec exercises. Any other call
// would panic on the nil embedded interface, which is the intent for this test.
type stubQuerier struct {
	database.Querier
	identifier uuid.UUID
}

func TestImageRefreshOptInAndReadinessProbe(t *testing.T) {
	var cfg models.Config
	err := yaml.Unmarshal([]byte(`services:
  - name: followed
    image: registry.example.com/app:latest
    imageAutoRefresh: true
    readinessProbe:
      httpGet:
        path: /healthz
        port: 8080
      periodSeconds: 5
  - name: manual
    image: registry.example.com/app:latest
`), &cfg)
	if err != nil {
		t.Fatal(err)
	}
	resolve := func(context.Context, string) (string, error) {
		return "registry.example.com/app@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", nil
	}
	if err := imageupdate.Prepare(context.Background(), cfg.Services, "abc", resolve); err != nil {
		t.Fatal(err)
	}
	for i := range cfg.Services {
		s := &cfg.Services[i]
		dep, err := GenerateDeploymentSpec(context.Background(), &models.DeployRequest{Namespace: "ns"}, s, &nimbusEnv.Env{})
		if err != nil {
			t.Fatal(err)
		}
		if i == 1 {
			if dep.Labels[imageupdate.Label] != "" || len(dep.Annotations) != 0 {
				t.Fatal("service opted in by default")
			}
			continue
		}
		if dep.Labels[imageupdate.Label] != "true" || dep.Annotations[imageupdate.SourceAnnotation] != "registry.example.com/app:latest" {
			t.Fatal("missing tracking metadata")
		}
		probe := dep.Spec.Template.Spec.Containers[0].ReadinessProbe
		if probe == nil || probe.HTTPGet == nil || probe.HTTPGet.Path != "/healthz" || probe.HTTPGet.Port.IntVal != 8080 || probe.PeriodSeconds != 5 {
			t.Fatalf("readiness probe not decoded: %+v", probe)
		}
	}
}

func TestDisableImageRefreshRemovesTrackingMetadata(t *testing.T) {
	previousClient := client
	client = fake.NewSimpleClientset()
	t.Cleanup(func() { client = previousClient })
	dep := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "app", Namespace: "ns"}}
	setImageTracking(dep, "registry.example.com/app:latest", "app")
	dep.Annotations["other-owner"] = "keep"
	dep.Annotations[imageupdate.ErrorAnnotation] = "lookup failed"
	if _, err := client.AppsV1().Deployments("ns").Create(context.Background(), dep, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	replacement := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "app", Namespace: "ns"}}
	got, err := CreateDeployment(context.Background(), "ns", replacement)
	if err != nil {
		t.Fatal(err)
	}
	if got.Labels[imageupdate.Label] != "" {
		t.Fatal("disabled deployment remains discoverable by worker")
	}
	for _, key := range imageupdate.MetadataKeys {
		if _, ok := got.Annotations[key]; ok {
			t.Fatalf("tracking field retained: %s", key)
		}
	}
	if got.Annotations["other-owner"] != "keep" {
		t.Fatal("unrelated metadata changed")
	}
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
