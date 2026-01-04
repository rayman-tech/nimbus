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

	// Get project
	env.Logger.DebugContext(ctx, "getting project")
	project, err := env.Database.GetProjectByName(ctx, request.Params.Project)
	if errors.Is(err, pgx.ErrNoRows) {
		env.Logger.ErrorContext(ctx, "failed to get project", slog.Any("error", err))
		return DeleteBranch404JSONResponse{
			Status:  apierror.ProjectNotFound.Status(),
			Code:    apierror.ProjectNotFound.String(),
			Message: "project not found",
			ErrorId: requestid,
		}, nil
	}
	if err != nil {
		env.Logger.ErrorContext(ctx, "failed to get project", slog.Any("error", err))
		return DeleteBranch500JSONResponse{
			Status:  apierror.InternalServerError.Status(),
			Code:    apierror.InternalServerError.String(),
			Message: "Internal Server Error",
			ErrorId: requestid,
		}, nil
	}

	// Check permissions
	env.Logger.DebugContext(ctx, "getting user permissions")
	authorized, err := env.Database.IsUserInProject(ctx, database.IsUserInProjectParams{
		UserID:    user.ID,
		ProjectID: project.ID,
	})
	if err != nil {
		env.Logger.ErrorContext(
			ctx, "failed to get user permissions", slog.Any("error", err))
		return DeleteBranch500JSONResponse{
			Status:  apierror.InternalServerError.Status(),
			Code:    apierror.InternalServerError.String(),
			Message: "Internal Server Error",
			ErrorId: requestid,
		}, nil
	}
	if !authorized {
		env.Logger.ErrorContext(ctx, "user does not have permissions")
		return DeleteBranch403JSONResponse{
			Status:  apierror.InsufficientPermissions.Status(),
			Code:    apierror.InsufficientPermissions.String(),
			Message: "user does not have permission to delete branch",
			ErrorId: requestid,
		}, nil
	}

	// Get services
	env.Logger.DebugContext(ctx, "getting services")
	services, err := env.Database.GetKubernetesServicesByProject(
		ctx,
		database.GetKubernetesServicesByProjectParams{
			ProjectID: project.ID, ProjectBranch: request.Params.Branch,
		})
	if err != nil {
		env.Logger.ErrorContext(ctx, "failed to get services", slog.Any("error", err))
		return DeleteBranch500JSONResponse{
			Status:  apierror.InternalServerError.Status(),
			Code:    apierror.InternalServerError.String(),
			Message: "Internal Server Error",
			ErrorId: requestid,
		}, nil
	}

	// Delete resources
	env.Logger.DebugContext(ctx, "deleting resources")
	namespace := utils.GetSanitizedNamespace(project.Name, request.Params.Branch)
	for _, svc := range services {
		err = kubernetes.DeleteDeployment(ctx, namespace, svc.ServiceName, env)
		if err != nil {
			env.Logger.ErrorContext(ctx, "failed to delete deployment", slog.Any("error", err))
		}
		err = kubernetes.DeleteService(ctx, namespace, svc.ServiceName, env)
		if err != nil {
			env.Logger.ErrorContext(ctx, "failed to delete service", slog.Any("error", err))
		}
		if svc.Ingress.Valid {
			err = kubernetes.DeleteIngress(ctx, namespace, svc.Ingress.String, env)
			if err != nil {
				env.Logger.ErrorContext(ctx, "failed to delete ingress", slog.Any("error", err))
			}
		}
		err = env.Database.DeleteKubernetesServiceById(ctx, svc.ID)
		if err != nil {
			env.Logger.ErrorContext(ctx, "failed to delete service", slog.Any("error", err))
			return DeleteBranch500JSONResponse{
				Status:  apierror.InternalServerError.Status(),
				Code:    apierror.InternalServerError.String(),
				Message: "Internal Server Error",
				ErrorId: requestid,
			}, nil
		}
	}

	ids, err := env.Database.GetUnusedKubernetesVolumeIdentifiers(
		ctx,
		database.GetUnusedKubernetesVolumeIdentifiersParams{
			ProjectID:      project.ID,
			ProjectBranch:  request.Params.Branch,
			ExcludeVolumes: nil,
		},
	)
	if err != nil {
		env.Logger.ErrorContext(ctx, "failed to delete volumes", slog.Any("error", err))
		return DeleteBranch500JSONResponse{
			Status:  apierror.InternalServerError.Status(),
			Code:    apierror.InternalServerError.String(),
			Message: "Internal Server Error",
			ErrorId: requestid,
		}, nil
	}
	for _, id := range ids {
		err = kubernetes.DeletePVC(ctx, namespace, fmt.Sprintf("pvc-%s", id.String()), env)
		if err != nil {
			env.Logger.ErrorContext(ctx, "failed to delete pvc", slog.Any("error", err))
		}
	}
	err = env.Database.DeleteUnusedKubernetesVolumes(ctx,
		database.DeleteUnusedKubernetesVolumesParams{
			ProjectID:      project.ID,
			ProjectBranch:  request.Params.Branch,
			ExcludeVolumes: nil,
		})
	if err != nil {
		env.Logger.ErrorContext(ctx, "failed to delete unused voumes", slog.Any("error", err))
		return DeleteBranch500JSONResponse{
			Status:  apierror.InternalServerError.Status(),
			Code:    apierror.InternalServerError.String(),
			Message: "Internal Server Error",
			ErrorId: requestid,
		}, nil
	}
	err = kubernetes.DeleteNamespace(ctx, namespace, env)
	if err != nil {
		env.Logger.ErrorContext(ctx, "failed to delete namespace", slog.Any("error", err))
		return DeleteBranch500JSONResponse{
			Status:  apierror.InternalServerError.Status(),
			Code:    apierror.InternalServerError.String(),
			Message: "Internal Server Error",
			ErrorId: requestid,
		}, nil
	}

	return DeleteBranch204Response{}, nil
}
