package openapi

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	apierror "nimbus/internal/api/error"
	"nimbus/internal/database"
	"nimbus/internal/env"
	"nimbus/internal/kubernetes"
	"nimbus/internal/utils"

	"github.com/google/uuid"
)

func authError(rid string) Error {
	return Error{
		Status:  apierror.InvalidAPIKey.Status(),
		Code:    apierror.InvalidAPIKey.String(),
		Message: "authentication required",
		ErrorId: rid,
	}
}

func internalError(rid string) Error {
	return Error{
		Status:  apierror.InternalServerError.Status(),
		Code:    apierror.InternalServerError.String(),
		Message: "Internal Server Error",
		ErrorId: rid,
	}
}

func forbiddenError(rid, msg string) Error {
	return Error{
		Status:  apierror.InsufficientPermissions.Status(),
		Code:    apierror.InsufficientPermissions.String(),
		Message: msg,
		ErrorId: rid,
	}
}

// deleteServiceResources deletes the k8s deployment, service, and ingress for a
// single service then removes the DB record. Cleanup failures keep the record
// so teardown can be retried.
func deleteServiceResources(
	ctx context.Context, namespace string, svc database.Service,
	db database.Querier,
) error {
	if err := deleteServiceKubernetesResources(ctx, namespace, svc); err != nil {
		return err
	}
	if err := db.DeleteServiceById(ctx, svc.ID); err != nil {
		return fmt.Errorf("deleting service %s from database: %w", svc.ServiceName, err)
	}
	return nil
}

func deleteServiceKubernetesResources(ctx context.Context, namespace string, svc database.Service) error {
	if err := kubernetes.DeletePublicRoute(ctx, namespace, svc.ServiceName, env.FromContext(ctx).Config); err != nil {
		return fmt.Errorf("removing public routing for %s: %w", svc.ServiceName, err)
	}
	if err := kubernetes.DeleteDeployment(ctx, namespace, svc.ServiceName); err != nil {
		return err
	}
	if err := kubernetes.DeleteService(ctx, namespace, svc.ServiceName); err != nil {
		return err
	}

	return nil
}

// deleteBranchResources deletes all services, unused volumes, and the
// namespace for a project branch.
func deleteBranchResources(
	ctx context.Context, namespace string, projectID uuid.UUID, branch string,
	db database.Querier,
) error {
	services, err := db.GetServicesByProject(ctx, database.GetServicesByProjectParams{
		ProjectID:     projectID,
		ProjectBranch: branch,
	})
	if err != nil {
		return fmt.Errorf("getting services: %w", err)
	}

	for _, svc := range services {
		if err := deleteServiceKubernetesResources(ctx, namespace, svc); err != nil {
			return err
		}
	}

	ids, err := db.GetUnusedVolumeIdentifiers(ctx, database.GetUnusedVolumeIdentifiersParams{
		ProjectID:      projectID,
		ProjectBranch:  branch,
		ExcludeVolumes: []string{},
	})
	if err != nil {
		return fmt.Errorf("getting unused volumes: %w", err)
	}
	return finishBranchCleanup(ctx, namespace, projectID, branch, ids, services, db, kubernetes.DeleteBranchStorage)
}

// Keep service and volume records until Kubernetes finishes cleanup. Project
// deletion can then rediscover even stateless branches after a failed attempt.
func finishBranchCleanup(ctx context.Context, namespace string, projectID uuid.UUID, branch string,
	ids []uuid.UUID, services []database.Service, db database.Querier,
	cleanup func(context.Context, string, []string, bool, time.Duration) error,
) error {
	names := make([]string, 0, len(ids))
	for _, id := range ids {
		names = append(names, fmt.Sprintf("pvc-%s", id))
	}
	timeout := env.FromContext(ctx).Config.CleanupTimeout
	if timeout <= 0 {
		timeout = 3 * time.Minute
	}
	preview := branch != "" && !utils.IsMainBranch(branch)
	if err := cleanup(ctx, namespace, names, preview, timeout); err != nil {
		return fmt.Errorf("cleaning branch storage: %w", err)
	}
	for _, svc := range services {
		if err := db.DeleteServiceById(ctx, svc.ID); err != nil {
			return fmt.Errorf("deleting service %s from database: %w", svc.ServiceName, err)
		}
	}
	if err := db.DeleteUnusedVolumes(ctx, database.DeleteUnusedVolumesParams{
		ProjectID: projectID, ProjectBranch: branch, ExcludeVolumes: []string{},
	}); err != nil {
		return fmt.Errorf("deleting unused volumes: %w", err)
	}

	return nil
}

// deleteStaleServices removes services that exist in the DB but are not present
// in the new deployment config. Cleanup errors retain the service record for retry.
func deleteStaleServices(
	ctx context.Context, namespace string,
	existingServices map[string]*database.Service, newNames map[string]bool,
	db database.Querier,
) error {
	for _, svc := range existingServices {
		if newNames[svc.ServiceName] {
			continue
		}
		slog.DebugContext(ctx, "deleting stale service", "service", svc.ServiceName, "namespace", namespace)
		if err := deleteServiceResources(ctx, namespace, *svc, db); err != nil {
			return err
		}
	}
	return nil
}
