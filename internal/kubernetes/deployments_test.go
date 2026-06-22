package kubernetes

import (
	"context"
	"testing"

	nimbusEnv "nimbus/internal/env"
	"nimbus/internal/models"
)

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
