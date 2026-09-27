package imageupdate

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"nimbus/internal/models"
)

var oldImage = "registry.example.com/app@sha256:" + strings.Repeat("a", 64)
var newImage = "registry.example.com/app@sha256:" + strings.Repeat("b", 64)

func TestPrepareImages(t *testing.T) {
	for _, tc := range []struct {
		name                  string
		service               models.Service
		commit, want          string
		wantLookup, wantError bool
	}{
		{name: "commit build", service: models.Service{Image: "registry.example.com/app"}, commit: "abc", want: "registry.example.com/app:abc"},
		{name: "legacy explicit tag", service: models.Service{Image: "registry.example.com/app:latest"}, commit: "abc", want: "registry.example.com/app:abc"},
		{name: "registry port", service: models.Service{Image: "registry.example.com:5000/app"}, commit: "abc", want: "registry.example.com:5000/app:abc"},
		{name: "CLI literal tag", service: models.Service{Image: "registry.example.com/app:stable"}, want: "registry.example.com/app:stable"},
		{name: "pinned rollback", service: models.Service{Image: oldImage}, commit: "abc", want: oldImage},
		{name: "follow ignores config commit", service: models.Service{Image: "registry.example.com/app:latest", ImageAutoRefresh: true}, commit: "abc", want: newImage, wantLookup: true},
		{name: "follow implicit latest", service: models.Service{Image: "registry.example.com/app", ImageAutoRefresh: true}, want: newImage, wantLookup: true},
		{name: "postgres template", service: models.Service{Template: "postgres"}},
		{name: "cannot follow postgres template", service: models.Service{Template: "postgres", ImageAutoRefresh: true}, wantError: true},
		{name: "cannot follow redis template", service: models.Service{Template: "redis", ImageAutoRefresh: true}, wantError: true},
		{name: "cannot follow digest", service: models.Service{Image: oldImage, ImageAutoRefresh: true}, wantError: true},
		{name: "missing followed image", service: models.Service{ImageAutoRefresh: true}, wantError: true},
		{name: "invalid image", service: models.Service{Image: "https://registry.example.com/app", ImageAutoRefresh: true}, wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			services := []models.Service{tc.service}
			calls := 0
			err := Prepare(context.Background(), services, tc.commit, func(_ context.Context, source string) (string, error) {
				calls++
				if source != "registry.example.com/app:latest" {
					t.Fatalf("source = %s", source)
				}
				return newImage, nil
			})
			if (err != nil) != tc.wantError {
				t.Fatalf("error = %v", err)
			}
			if tc.wantError {
				return
			}
			if services[0].Image != tc.want {
				t.Fatalf("image = %q, want %q", services[0].Image, tc.want)
			}
			if (calls > 0) != tc.wantLookup {
				t.Fatalf("registry calls = %d", calls)
			}
			if tc.wantLookup && services[0].ImageSource != "registry.example.com/app:latest" {
				t.Fatal("lost followed tag")
			}
		})
	}
}

func TestPrepareSharesLookupAndPropagatesRegistryFailure(t *testing.T) {
	services := []models.Service{
		{Image: "registry.example.com/app:latest", ImageAutoRefresh: true},
		{Image: "registry.example.com/app:latest", ImageAutoRefresh: true},
	}
	calls := 0
	err := Prepare(context.Background(), services, "abc", func(context.Context, string) (string, error) { calls++; return newImage, nil })
	if err != nil || calls != 1 || services[1].Image != newImage {
		t.Fatalf("calls=%d, error=%v", calls, err)
	}
	services[0].Image = "registry.example.com/app:latest"
	err = Prepare(context.Background(), services[:1], "", func(context.Context, string) (string, error) { return "", fmt.Errorf("unauthorized") })
	if err == nil {
		t.Fatal("registry failure was ignored")
	}
}

func TestResolveAuthenticatedMultiArchIndex(t *testing.T) {
	// The index digest must be followed, not either platform's manifest digest.
	manifest := `{"schemaVersion":2,"mediaType":"application/vnd.oci.image.index.v1+json","manifests":[{"mediaType":"application/vnd.oci.image.manifest.v1+json","digest":"sha256:` + strings.Repeat("a", 64) + `","size":123,"platform":{"architecture":"amd64","os":"linux"}},{"mediaType":"application/vnd.oci.image.manifest.v1+json","digest":"sha256:` + strings.Repeat("b", 64) + `","size":123,"platform":{"architecture":"arm64","os":"linux"}}]}`
	digest := fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(manifest)))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, password, ok := r.BasicAuth()
		if !ok || user != "reader" || password != "test-password" {
			w.Header().Set("WWW-Authenticate", `Basic realm="registry"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/v2/":
			w.WriteHeader(http.StatusOK)
		case "/v2/app/manifests/latest":
			w.Header().Set("Content-Type", "application/vnd.oci.image.index.v1+json")
			w.Header().Set("Docker-Content-Digest", digest)
			_, _ = w.Write([]byte(manifest))
		default:
			t.Errorf("unexpected registry request (must not download layers): %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	host := strings.TrimPrefix(server.URL, "http://")
	dir := t.TempDir()
	t.Setenv("DOCKER_CONFIG", dir)
	auth := base64.StdEncoding.EncodeToString([]byte("reader:test-password"))
	config := fmt.Sprintf(`{"auths":{%q:{"auth":%q}}}`, host, auth)
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := Resolve(context.Background(), host+"/app:latest")
	if err != nil || got != host+"/app@"+digest {
		t.Fatalf("image = %q, error = %v", got, err)
	}
	// Rotation/removal must be observed without restarting Nimbus.
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"auths":{}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Resolve(context.Background(), host+"/app:latest"); err == nil {
		t.Fatal("unauthenticated registry access succeeded")
	}
}
