package openapi

import (
	"context"
	"errors"
	"testing"
	"time"

	"nimbus/internal/config"
	"nimbus/internal/database"
	"nimbus/internal/env"

	"github.com/google/uuid"
)

type cleanupDB struct {
	database.Querier
	deleted        bool
	serviceDeleted bool
	deleteError    error
}

func (d *cleanupDB) DeleteServiceById(context.Context, uuid.UUID) error {
	d.serviceDeleted = true
	return nil
}

func (d *cleanupDB) DeleteUnusedVolumes(ctx context.Context, p database.DeleteUnusedVolumesParams) error {
	if p.ExcludeVolumes == nil {
		panic("nil exclusions become SQL NULL, not an empty array")
	}
	d.deleted = true
	return d.deleteError
}
func (d *cleanupDB) GetServicesByProject(context.Context, database.GetServicesByProjectParams) ([]database.Service, error) {
	return nil, nil
}
func (d *cleanupDB) GetUnusedVolumeIdentifiers(ctx context.Context, p database.GetUnusedVolumeIdentifiersParams) ([]uuid.UUID, error) {
	if p.ExcludeVolumes == nil {
		panic("nil exclusions skip volume records")
	}
	return nil, errors.New("stop after checking exclusions")
}
func TestCleanupQueriesAllVolumeRecords(t *testing.T) {
	if err := deleteBranchResources(context.Background(), "preview", uuid.New(), "feature", &cleanupDB{}); err == nil {
		t.Fatal("expected injected lookup error")
	}
}
func TestCleanupKeepsRecordsUntilStorageIsGone(t *testing.T) {
	for _, branch := range []string{"", "main", "master", "feature/test"} {
		t.Run(branch, func(t *testing.T) {
			ctx := env.WithContext(context.Background(), &env.Env{Config: &config.Config{CleanupTimeout: time.Second}})
			db := &cleanupDB{}
			id := uuid.New()
			failure := errors.New("provisioner not finished")
			calls := 0
			cleanup := func(ctx context.Context, ns string, names []string, preview bool, timeout time.Duration) error {
				calls++
				if db.deleted || db.serviceDeleted {
					t.Fatal("records removed before cleanup completed")
				}
				if preview != (branch == "feature/test") {
					t.Fatalf("wrong production/preview classification for %q", branch)
				}
				if len(names) != 1 || names[0] != "pvc-"+id.String() {
					t.Fatal("missing retry volume identity")
				}
				if timeout != time.Second {
					t.Fatal("cleanup timeout not passed through")
				}
				if calls == 1 {
					return failure
				}
				return nil
			}
			if err := finishBranchCleanup(ctx, "ns", uuid.New(), branch, []uuid.UUID{id}, []database.Service{{ID: uuid.New()}}, db, cleanup); !errors.Is(err, failure) {
				t.Fatalf("got %v", err)
			}
			if db.deleted || db.serviceDeleted {
				t.Fatal("failed cleanup lost records")
			}
			if err := finishBranchCleanup(ctx, "ns", uuid.New(), branch, []uuid.UUID{id}, []database.Service{{ID: uuid.New()}}, db, cleanup); err != nil {
				t.Fatal(err)
			}
			if !db.deleted || !db.serviceDeleted {
				t.Fatal("successful cleanup retained records")
			}
		})
	}
}
