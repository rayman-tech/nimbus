package openapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"

	apierror "nimbus/internal/api/error"
	"nimbus/internal/api/requestid"
	"nimbus/internal/database"
	"nimbus/internal/env"
	"nimbus/internal/kubernetes"
	"nimbus/internal/utils"

	"github.com/jackc/pgx/v5"
)

func (Server) GetProjects(
	ctx context.Context, request GetProjectsRequestObject,
) (GetProjectsResponseObject, error) {
	env := env.FromContext(ctx)
	requestid := fmt.Sprintf("%d", requestid.FromContext(ctx))
	user := database.UserFromContext(ctx)

	dbProjects, err := env.Database.GetProjectsByUser(ctx, user.ID)
	if err != nil {
		env.Logger.ErrorContext(ctx, "failed to get projects", slog.Any("error", err))
		return GetProjects500JSONResponse{
			Status:  apierror.InternalServerError.Status(),
			Code:    apierror.InternalServerError.String(),
			Message: "Internal Server Error",
			ErrorId: requestid,
		}, nil
	}

	projects := make([]Project, len(dbProjects))
	for i, project := range dbProjects {
		projects[i] = Project{
			Id:   &project.ID,
			Name: &project.Name,
		}
	}

	return GetProjects200JSONResponse{
		Projects: &projects,
	}, nil
}

func (Server) PostProjects(
	ctx context.Context, request PostProjectsRequestObject,
) (PostProjectsResponseObject, error) {
	return PostProjects201JSONResponse{}, nil
}

func (Server) DeleteProjectsName(
	ctx context.Context, request DeleteProjectsNameRequestObject,
) (DeleteProjectsNameResponseObject, error) {
	env := env.FromContext(ctx)
	requestid := fmt.Sprintf("%d", requestid.FromContext(ctx))
	user := database.UserFromContext(ctx)

	// Get project
	env.Logger.DebugContext(ctx, "getting project")
	project, err := env.Database.GetProjectByName(ctx, request.Name)
	if errors.Is(err, pgx.ErrNoRows) {
		env.Logger.ErrorContext(ctx, "failed to get project", slog.Any("error", err))
		return DeleteProjectsName404JSONResponse{
			Status:  apierror.ProjectNotFound.Status(),
			Code:    apierror.ProjectNotFound.String(),
			Message: "project not found",
			ErrorId: requestid,
		}, nil
	}
	if err != nil {
		env.Logger.ErrorContext(ctx, "failed to get project", slog.Any("error", err))
		return DeleteProjectsName500JSONResponse{
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
		return DeleteProjectsName500JSONResponse{
			Status:  apierror.InternalServerError.Status(),
			Code:    apierror.InternalServerError.String(),
			Message: "Internal Server Error",
			ErrorId: requestid,
		}, nil
	}
	if !authorized {
		env.Logger.ErrorContext(ctx, "user does not have permissions")
		return DeleteProjectsName403JSONResponse{
			Status:  apierror.InsufficientPermissions.Status(),
			Code:    apierror.InsufficientPermissions.String(),
			Message: "user does not have permission to delete branch",
			ErrorId: requestid,
		}, nil
	}

	// Get project branches
	env.Logger.DebugContext(ctx, "getting project branches")
	branches, err := env.Database.GetKubernetesProjectBranches(ctx, project.ID)
	if err != nil {
		env.Logger.ErrorContext(ctx, "failed to get project branches", slog.Any("error", err))
		return DeleteProjectsName500JSONResponse{
			Status:  apierror.InternalServerError.Status(),
			Code:    apierror.InternalServerError.String(),
			Message: "Internal Server Error",
			ErrorId: requestid,
		}, nil
	}

	for _, branch := range branches {
		services, err := env.Database.GetKubernetesServicesByProject(
			ctx,
			database.GetKubernetesServicesByProjectParams{
				ProjectID:     project.ID,
				ProjectBranch: branch,
			})
		if err != nil {
			env.Logger.DebugContext(ctx, "failed to get services", slog.Any("error", err))
			return DeleteProjectsName500JSONResponse{
				Status:  apierror.InternalServerError.Status(),
				Code:    apierror.InternalServerError.String(),
				Message: "Internal Server Error",
				ErrorId: requestid,
			}, nil
		}

		// Delete services
		namespace := utils.GetSanitizedNamespace(project.Name, branch)
		for _, svc := range services {
			err = kubernetes.DeleteDeployment(ctx, namespace, svc.ServiceName, env)
			if err != nil {
				env.Logger.ErrorContext(ctx, "failed to delete deployment", slog.Any("error", err))
			}
			err = kubernetes.DeleteService(ctx, namespace, svc.ServiceName, env)
			if err != nil {
				env.Logger.ErrorContext(ctx, "failed to deleted service", slog.Any("error", err))
			}
			if svc.Ingress.Valid {
				err = kubernetes.DeleteIngress(ctx, namespace, svc.Ingress.String, env)
				if err != nil {
					env.Logger.ErrorContext(ctx, "failed to delete ingress", slog.Any("error", err))
				}
			}
			err = env.Database.DeleteKubernetesServiceById(ctx, svc.ID)
			if err != nil {
				env.Logger.ErrorContext(ctx, "failed to delete service from database", slog.Any("error", err))
				return DeleteProjectsName500JSONResponse{
					Status:  apierror.InternalServerError.Status(),
					Code:    apierror.InternalServerError.String(),
					Message: "Internal Server Error",
					ErrorId: requestid,
				}, nil
			}
		}

		// Delete volumes
		ids, err := env.Database.GetUnusedKubernetesVolumeIdentifiers(
			ctx,
			database.GetUnusedKubernetesVolumeIdentifiersParams{
				ProjectID: project.ID, ProjectBranch: branch, ExcludeVolumes: nil,
			})
		if err != nil {
			env.Logger.ErrorContext(ctx, "failed to get volumes", slog.Any("error", err))
			return DeleteProjectsName500JSONResponse{
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
		err = env.Database.DeleteUnusedKubernetesVolumes(
			ctx,
			database.DeleteUnusedKubernetesVolumesParams{
				ProjectID: project.ID, ProjectBranch: branch, ExcludeVolumes: nil,
			})
		if err != nil {
			env.Logger.ErrorContext(ctx, "failed to delete unused volumes", slog.Any("error", err))
			return DeleteProjectsName500JSONResponse{
				Status:  apierror.InternalServerError.Status(),
				Code:    apierror.InternalServerError.String(),
				Message: "Internal Server Error",
				ErrorId: requestid,
			}, nil
		}
		err = kubernetes.DeleteNamespace(ctx, namespace, env)
		if err != nil {
			env.Logger.ErrorContext(ctx, "failed to delete namespace", slog.Any("error", err))
			return DeleteProjectsName500JSONResponse{
				Status:  apierror.InternalServerError.Status(),
				Code:    apierror.InternalServerError.String(),
				Message: "Internal Server Error",
				ErrorId: requestid,
			}, nil
		}
	}

	err = env.Database.DeleteProject(ctx, project.ID)
	if err != nil {
		env.Logger.ErrorContext(ctx, "failed to delete project", slog.Any("error", err))
		return DeleteProjectsName500JSONResponse{
			Status:  apierror.InternalServerError.Status(),
			Code:    apierror.InternalServerError.String(),
			Message: "Internal Server Error",
			ErrorId: requestid,
		}, nil
	}

	return DeleteProjectsName204Response{}, nil
}

func (Server) GetProjectsNameSecrets(
	ctx context.Context, request GetProjectsNameSecretsRequestObject,
) (GetProjectsNameSecretsResponseObject, error) {
	env := env.FromContext(ctx)
	requestid := fmt.Sprintf("%d", requestid.FromContext(ctx))
	user := database.UserFromContext(ctx)

	// Get project
	env.Logger.DebugContext(ctx, "getting project")
	project, err := env.Database.GetProjectByName(ctx, request.Name)
	if errors.Is(err, pgx.ErrNoRows) {
		env.Logger.ErrorContext(ctx, "failed to get project", slog.Any("error", err))
		return GetProjectsNameSecrets404JSONResponse{
			Status:  apierror.ProjectNotFound.Status(),
			Code:    apierror.ProjectNotFound.String(),
			Message: "project not found",
			ErrorId: requestid,
		}, nil
	}
	if err != nil {
		env.Logger.ErrorContext(ctx, "failed to get project", slog.Any("error", err))
		return GetProjectsNameSecrets500JSONResponse{
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
		return GetProjectsNameSecrets500JSONResponse{
			Status:  apierror.InternalServerError.Status(),
			Code:    apierror.InternalServerError.String(),
			Message: "Internal Server Error",
			ErrorId: requestid,
		}, nil
	}
	if !authorized {
		env.Logger.ErrorContext(ctx, "user does not have permissions")
		return GetProjectsNameSecrets403JSONResponse{
			Status:  apierror.InsufficientPermissions.Status(),
			Code:    apierror.InsufficientPermissions.String(),
			Message: "user does not have permission to delete branch",
			ErrorId: requestid,
		}, nil
	}

	// Get secrets
	var res []byte
	env.Logger.DebugContext(ctx, "getting secrets")
	if request.Params.Values != nil && *request.Params.Values {
		vals, err := kubernetes.GetSecretValues(ctx, project.Name, env)
		if err != nil {
			env.Logger.ErrorContext(ctx, "failed to get secret values", slog.Any("error", err))
			return GetProjectsNameSecrets500JSONResponse{
				Status:  apierror.InternalServerError.Status(),
				Code:    apierror.InternalServerError.String(),
				Message: "Internal Server Error",
				ErrorId: requestid,
			}, nil
		}
		if vals == nil {
			vals = make(map[string]string)
		}
		res, err = json.Marshal(SecretsValuesResponse{Secrets: &vals})
		if err != nil {
			env.Logger.ErrorContext(ctx, "failed to marshal secret values", slog.Any("error", err))
			return GetProjectsNameSecrets500JSONResponse{
				Status:  apierror.InternalServerError.Status(),
				Code:    apierror.InternalServerError.String(),
				Message: "Internal Server Error",
				ErrorId: requestid,
			}, nil
		}
	} else {
		names, err := kubernetes.ListSecretNames(ctx, project.Name, env)
		if err != nil {
			env.Logger.ErrorContext(ctx, "failed to get secret names", slog.Any("error", err))
			return GetProjectsNameSecrets500JSONResponse{
				Status:  apierror.InternalServerError.Status(),
				Code:    apierror.InternalServerError.String(),
				Message: "Internal Server Error",
				ErrorId: requestid,
			}, nil
		}
		if names == nil {
			names = make([]string, 0)
		}
		res, err = json.Marshal(SecretsNamesResponse{Secrets: &names})
		if err != nil {
			env.Logger.ErrorContext(ctx, "failed to marshal secret names", slog.Any("error", err))
			return GetProjectsNameSecrets500JSONResponse{
				Status:  apierror.InternalServerError.Status(),
				Code:    apierror.InternalServerError.String(),
				Message: "Internal Server Error",
				ErrorId: requestid,
			}, nil
		}
	}

	return GetProjectsNameSecrets200JSONResponse{union: res}, nil
}

func (Server) PutProjectsNameSecrets(
	ctx context.Context, request PutProjectsNameSecretsRequestObject,
) (PutProjectsNameSecretsResponseObject, error) {
	env := env.FromContext(ctx)
	requestid := fmt.Sprintf("%d", requestid.FromContext(ctx))
	user := database.UserFromContext(ctx)

	// Get project
	env.Logger.DebugContext(ctx, "getting project")
	project, err := env.Database.GetProjectByName(ctx, request.Name)
	if errors.Is(err, pgx.ErrNoRows) {
		env.Logger.ErrorContext(ctx, "failed to get project", slog.Any("error", err))
		return PutProjectsNameSecrets404JSONResponse{
			Status:  apierror.ProjectNotFound.Status(),
			Code:    apierror.ProjectNotFound.String(),
			Message: "project not found",
			ErrorId: requestid,
		}, nil
	}
	if err != nil {
		env.Logger.ErrorContext(ctx, "failed to get project", slog.Any("error", err))
		return PutProjectsNameSecrets500JSONResponse{
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
		return PutProjectsNameSecrets500JSONResponse{
			Status:  apierror.InternalServerError.Status(),
			Code:    apierror.InternalServerError.String(),
			Message: "Internal Server Error",
			ErrorId: requestid,
		}, nil
	}
	if !authorized {
		env.Logger.ErrorContext(ctx, "user does not have permissions")
		return PutProjectsNameSecrets403JSONResponse{
			Status:  apierror.InsufficientPermissions.Status(),
			Code:    apierror.InsufficientPermissions.String(),
			Message: "user does not have permission to delete branch",
			ErrorId: requestid,
		}, nil
	}

	// Get project branches
	env.Logger.DebugContext(ctx, "getting project branches")
	branches, err := env.Database.GetKubernetesProjectBranches(ctx, project.ID)
	if err != nil {
		env.Logger.ErrorContext(ctx, "failed to get project branches", slog.Any("error", err))
		return PutProjectsNameSecrets500JSONResponse{
			Status:  apierror.InternalServerError.Status(),
			Code:    apierror.InternalServerError.String(),
			Message: "Internal Server Error",
			ErrorId: requestid,
		}, nil
	}
	if len(branches) == 0 {
		branches = []string{"main"}
	}
	if !slices.Contains(branches, "main") && !slices.Contains(branches, "master") {
		branches = append(branches, "main")
	}

	var secrets map[string]string
	if request.Body.Secrets != nil {
		secrets = *request.Body.Secrets
	} else {
		secrets = make(map[string]string)
	}
	for _, branch := range branches {
		namespace := utils.GetSanitizedNamespace(project.Name, branch)
		env.Logger.DebugContext(ctx, "Updating secrets for namespace", slog.String("namespace", namespace))
		err = kubernetes.UpdateSecret(
			ctx, namespace, fmt.Sprintf("%s-env", project.Name), secrets, env,
		)
		if err != nil {
			env.Logger.ErrorContext(ctx, "failed to update secrets", slog.Any("error", err))
			return PutProjectsNameSecrets500JSONResponse{
				Status:  apierror.InternalServerError.Status(),
				Code:    apierror.InternalServerError.String(),
				Message: "Internal Server Error",
				ErrorId: requestid,
			}, nil
		}
	}

	return PutProjectsNameSecrets200Response{}, nil
}
