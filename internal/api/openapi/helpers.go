package openapi

import (
	"context"
	"fmt"
	"log/slog"

	apierror "nimbus/internal/api/error"
	"nimbus/internal/database"
	"nimbus/internal/kubernetes"

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
// single service then removes the DB record. K8s errors are logged but not
// fatal; a DB deletion error is returned.
func deleteServiceResources(
	ctx context.Context, namespace string, svc database.Service,
	db database.Querier,
) error {
	if err := kubernetes.DeleteDeployment(ctx, namespace, svc.ServiceName); err != nil {
		slog.ErrorContext(ctx, "failed to delete deployment", "service", svc.ServiceName, "error", err)
	}
	if err := kubernetes.DeleteService(ctx, namespace, svc.ServiceName); err != nil {
		slog.ErrorContext(ctx, "failed to delete service", "service", svc.ServiceName, "error", err)
	}
	if svc.Ingress.Valid {
		if err := kubernetes.DeleteIngress(ctx, namespace, svc.Ingress.String); err != nil {
			slog.ErrorContext(ctx, "failed to delete ingress", "service", svc.ServiceName, "error", err)
		}
	}
	if err := db.DeleteServiceById(ctx, svc.ID); err != nil {
		return fmt.Errorf("deleting service %s from database: %w", svc.ServiceName, err)
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
		if err := deleteServiceResources(ctx, namespace, svc, db); err != nil {
			return err
		}
	}

	ids, err := db.GetUnusedVolumeIdentifiers(ctx, database.GetUnusedVolumeIdentifiersParams{
		ProjectID:      projectID,
		ProjectBranch:  branch,
		ExcludeVolumes: nil,
	})
	if err != nil {
		return fmt.Errorf("getting unused volumes: %w", err)
	}
	for _, id := range ids {
		if err := kubernetes.DeletePVC(ctx, namespace, fmt.Sprintf("pvc-%s", id.String())); err != nil {
			slog.ErrorContext(ctx, "failed to delete pvc", "error", err)
		}
	}
	if err := db.DeleteUnusedVolumes(ctx, database.DeleteUnusedVolumesParams{
		ProjectID:      projectID,
		ProjectBranch:  branch,
		ExcludeVolumes: nil,
	}); err != nil {
		return fmt.Errorf("deleting unused volumes: %w", err)
	}

	if err := kubernetes.DeleteNamespace(ctx, namespace); err != nil {
		slog.ErrorContext(ctx, "failed to delete namespace", "namespace", namespace, "error", err)
	}

	return nil
}

// deleteStaleServices removes services that exist in the DB but are not present
// in the new deployment config. K8s errors are logged but not fatal.
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
