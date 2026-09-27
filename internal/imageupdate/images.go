// Package imageupdate follows registry tags for explicitly opted-in services.
package imageupdate

import (
	"context"
	"fmt"
	"time"

	"nimbus/internal/models"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/remote"
)

// ResolveFunc returns the immutable image reference behind a tag.
type ResolveFunc func(context.Context, string) (string, error)

// Resolve reads the top-level manifest/index, never image layers. Docker's
// standard credential configuration can be mounted using DOCKER_CONFIG.
func Resolve(ctx context.Context, source string) (string, error) {
	ref, err := name.NewTag(source)
	if err != nil {
		return "", err
	}
	const lookupTimeout = 5 * time.Second
	ctx, cancel := context.WithTimeout(ctx, lookupTimeout)
	defer cancel()
	desc, err := remote.Get(ref, remote.WithContext(ctx), remote.WithAuthFromKeychain(authn.DefaultKeychain))
	if err != nil {
		return "", err
	}
	return ref.Context().Digest(desc.Digest.String()).Name(), nil
}

// Prepare validates every reference before looking up followed images. Existing
// commit-tag deployments retain their behavior; explicit digests stay pinned.
func Prepare(ctx context.Context, services []models.Service, commit string, resolve ResolveFunc) error {
	for i := range services {
		s := &services[i]
		if s.ImageAutoRefresh && (s.Template == "postgres" || s.Template == "redis") {
			return fmt.Errorf("service %q: imageAutoRefresh requires an explicit image, not a database template", s.Name)
		}
		if s.Template == "postgres" || s.Template == "redis" || (!s.ImageAutoRefresh && s.Image == "") {
			continue
		}
		ref, err := name.ParseReference(s.Image)
		if err != nil {
			return fmt.Errorf("service %q: invalid image reference: %w", s.Name, err)
		}
		if s.ImageAutoRefresh {
			if _, ok := ref.(name.Tag); !ok {
				return fmt.Errorf("service %q: imageAutoRefresh requires a tag, not a digest", s.Name)
			}
			s.ImageSource = ref.Name()
		} else if _, pinned := ref.(name.Digest); !pinned && commit != "" {
			tag, err := name.NewTag(ref.Context().Name() + ":" + commit)
			if err != nil {
				return fmt.Errorf("service %q: invalid commit image tag: %w", s.Name, err)
			}
			s.Image = tag.Name()
		}
	}
	resolved := make(map[string]string)
	for i := range services {
		s := &services[i]
		if !s.ImageAutoRefresh {
			continue
		}
		image, ok := resolved[s.ImageSource]
		if !ok {
			var err error
			image, err = resolve(ctx, s.ImageSource)
			if err != nil {
				return fmt.Errorf("service %q: resolving followed image: %w", s.Name, err)
			}
			resolved[s.ImageSource] = image
		}
		s.Image = image
	}
	return nil
}
