package openapi

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	apierror "nimbus/internal/api/error"
	"nimbus/internal/api/requestid"
	"nimbus/internal/database"
	"nimbus/internal/env"
	"nimbus/internal/kubernetes"
	"nimbus/internal/utils"

	"github.com/jackc/pgx/v5"
)

func (Server) DeleteBranch(ctx context.Context, request DeleteBranchRequestObject) (DeleteBranchResponseObject, error) {
	env := env.FromContext(ctx)
	requestid := fmt.Sprintf("%d", requestid.FromContext(ctx))
	user := database.UserFromContext(ctx)
	if user == nil {
		return DeleteBranch401JSONResponse{
			Status:  apierror.InvalidAPIKey.Status(),
			Code:    apierror.InvalidAPIKey.String(),
			Message: "authentication required",
			ErrorId: requestid,
		}, nil
	}

	// Get project
	slog.DebugContext(ctx, "getting project")
	project, err := env.Database.GetProjectByName(ctx, request.Params.Project)
	if errors.Is(err, pgx.ErrNoRows) {
		slog.ErrorContext(ctx, "failed to get project", "error", err)
		return DeleteBranch404JSONResponse{
			Status:  apierror.ProjectNotFound.Status(),
			Code:    apierror.ProjectNotFound.String(),
			Message: "project not found",
			ErrorId: requestid,
		}, nil
	}
	if err != nil {
		slog.ErrorContext(ctx, "failed to get project", "error", err)
		return DeleteBranch500JSONResponse{
			Status:  apierror.InternalServerError.Status(),
			Code:    apierror.InternalServerError.String(),
			Message: "Internal Server Error",
			ErrorId: requestid,
		}, nil
	}

	// Check permissions
	slog.DebugContext(ctx, "getting user permissions")
	authorized, err := env.Database.IsUserInProject(ctx, database.IsUserInProjectParams{
		UserID:    user.ID,
		ProjectID: project.ID,
	})
	if err != nil {
		slog.ErrorContext(
			ctx, "failed to get user permissions", "error", err)
		return DeleteBranch500JSONResponse{
			Status:  apierror.InternalServerError.Status(),
			Code:    apierror.InternalServerError.String(),
			Message: "Internal Server Error",
			ErrorId: requestid,
		}, nil
	}
	if !authorized {
		slog.ErrorContext(ctx, "user does not have permissions")
		return DeleteBranch403JSONResponse{
			Status:  apierror.InsufficientPermissions.Status(),
			Code:    apierror.InsufficientPermissions.String(),
			Message: "user does not have permission to delete branch",
			ErrorId: requestid,
		}, nil
	}

	// Get services
	slog.DebugContext(ctx, "getting services")
	services, err := env.Database.GetServicesByProject(
		ctx,
		database.GetServicesByProjectParams{
			ProjectID: project.ID, ProjectBranch: request.Params.Branch,
		})
	if err != nil {
		slog.ErrorContext(ctx, "failed to get services", "error", err)
		return DeleteBranch500JSONResponse{
			Status:  apierror.InternalServerError.Status(),
			Code:    apierror.InternalServerError.String(),
			Message: "Internal Server Error",
			ErrorId: requestid,
		}, nil
	}

	// Delete resources
	slog.DebugContext(ctx, "deleting resources")
	namespace := utils.GetSanitizedNamespace(project.Name, request.Params.Branch)
	for _, svc := range services {
		err = kubernetes.DeleteDeployment(ctx, namespace, svc.ServiceName, env.Config)
		if err != nil {
			slog.ErrorContext(ctx, "failed to delete deployment", "error", err)
		}
		err = kubernetes.DeleteService(ctx, namespace, svc.ServiceName, env.Config)
		if err != nil {
			slog.ErrorContext(ctx, "failed to delete service", "error", err)
		}
		if svc.Ingress.Valid {
			err = kubernetes.DeleteIngress(ctx, namespace, svc.Ingress.String, env.Config)
			if err != nil {
				slog.ErrorContext(ctx, "failed to delete ingress", "error", err)
			}
		}
		err = env.Database.DeleteServiceById(ctx, svc.ID)
		if err != nil {
			slog.ErrorContext(ctx, "failed to delete service", "error", err)
			return DeleteBranch500JSONResponse{
				Status:  apierror.InternalServerError.Status(),
				Code:    apierror.InternalServerError.String(),
				Message: "Internal Server Error",
				ErrorId: requestid,
			}, nil
		}
	}

	ids, err := env.Database.GetUnusedVolumeIdentifiers(
		ctx,
		database.GetUnusedVolumeIdentifiersParams{
			ProjectID:      project.ID,
			ProjectBranch:  request.Params.Branch,
			ExcludeVolumes: nil,
		},
	)
	if err != nil {
		slog.ErrorContext(ctx, "failed to delete volumes", "error", err)
		return DeleteBranch500JSONResponse{
			Status:  apierror.InternalServerError.Status(),
			Code:    apierror.InternalServerError.String(),
			Message: "Internal Server Error",
			ErrorId: requestid,
		}, nil
	}
	for _, id := range ids {
		err = kubernetes.DeletePVC(ctx, namespace, fmt.Sprintf("pvc-%s", id.String()), env.Config)
		if err != nil {
			slog.ErrorContext(ctx, "failed to delete pvc", "error", err)
		}
	}
	err = env.Database.DeleteUnusedVolumes(ctx,
		database.DeleteUnusedVolumesParams{
			ProjectID:      project.ID,
			ProjectBranch:  request.Params.Branch,
			ExcludeVolumes: nil,
		})
	if err != nil {
		slog.ErrorContext(ctx, "failed to delete unused voumes", "error", err)
		return DeleteBranch500JSONResponse{
			Status:  apierror.InternalServerError.Status(),
			Code:    apierror.InternalServerError.String(),
			Message: "Internal Server Error",
			ErrorId: requestid,
		}, nil
	}
	err = kubernetes.DeleteNamespace(ctx, namespace, env.Config)
	if err != nil {
		slog.ErrorContext(ctx, "failed to delete namespace", "error", err)
		return DeleteBranch500JSONResponse{
			Status:  apierror.InternalServerError.Status(),
			Code:    apierror.InternalServerError.String(),
			Message: "Internal Server Error",
			ErrorId: requestid,
		}, nil
	}

	return DeleteBranch204Response{}, nil
}
