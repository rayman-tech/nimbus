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

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (Server) GetProjects(
	ctx context.Context, request GetProjectsRequestObject,
) (GetProjectsResponseObject, error) {
	env := env.FromContext(ctx)
	requestid := fmt.Sprintf("%d", requestid.FromContext(ctx))
	user := database.UserFromContext(ctx)
	if user == nil {
		return GetProjects401JSONResponse{
			Status:  apierror.InvalidAPIKey.Status(),
			Code:    apierror.InvalidAPIKey.String(),
			Message: "authentication required",
			ErrorId: requestid,
		}, nil
	}

	dbProjects, err := env.Database.GetProjectsByUser(ctx, user.ID)
	if err != nil {
		slog.ErrorContext(ctx, "failed to get projects", "error", err)
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
	env := env.FromContext(ctx)
	requestid := fmt.Sprintf("%d", requestid.FromContext(ctx))
	user := database.UserFromContext(ctx)
	if user == nil {
		return PostProjects401JSONResponse{
			Status:  apierror.InvalidAPIKey.Status(),
			Code:    apierror.InvalidAPIKey.String(),
			Message: "authentication required",
			ErrorId: requestid,
		}, nil
	}

	if request.Body == nil || request.Body.Name == "" {
		return PostProjects400JSONResponse{
			Status:  apierror.BadRequest.Status(),
			Code:    apierror.BadRequest.String(),
			Message: "project name is required",
			ErrorId: requestid,
		}, nil
	}

	// Check if project already exists
	_, err := env.Database.GetProjectByName(ctx, request.Body.Name)
	if err == nil {
		return PostProjects400JSONResponse{
			Status:  apierror.BadRequest.Status(),
			Code:    apierror.BadRequest.String(),
			Message: "project already exists",
			ErrorId: requestid,
		}, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		slog.ErrorContext(ctx, "failed to check project existence", "error", err)
		return PostProjects500JSONResponse{
			Status:  apierror.InternalServerError.Status(),
			Code:    apierror.InternalServerError.String(),
			Message: "Internal Server Error",
			ErrorId: requestid,
		}, nil
	}

	// Create the project
	slog.DebugContext(ctx, "creating project", "name", request.Body.Name)
	projectID := uuid.New()
	project, err := env.Database.CreateProject(ctx, database.CreateProjectParams{
		ID:   projectID,
		Name: request.Body.Name,
	})
	if err != nil {
		slog.ErrorContext(ctx, "failed to create project", "error", err)
		return PostProjects500JSONResponse{
			Status:  apierror.InternalServerError.Status(),
			Code:    apierror.InternalServerError.String(),
			Message: "Internal Server Error",
			ErrorId: requestid,
		}, nil
	}

	// Add the creating user to the project
	err = env.Database.AddUserToProject(ctx, database.AddUserToProjectParams{
		UserID:    user.ID,
		ProjectID: project.ID,
	})
	if err != nil {
		slog.ErrorContext(ctx, "failed to add user to project", "error", err)
		return PostProjects500JSONResponse{
			Status:  apierror.InternalServerError.Status(),
			Code:    apierror.InternalServerError.String(),
			Message: "Internal Server Error",
			ErrorId: requestid,
		}, nil
	}

	return PostProjects201JSONResponse{
		Id:   &project.ID,
		Name: &project.Name,
	}, nil
}

func (Server) DeleteProjectsName(
	ctx context.Context, request DeleteProjectsNameRequestObject,
) (DeleteProjectsNameResponseObject, error) {
	env := env.FromContext(ctx)
	requestid := fmt.Sprintf("%d", requestid.FromContext(ctx))
	user := database.UserFromContext(ctx)
	if user == nil {
		return DeleteProjectsName401JSONResponse{
			Status:  apierror.InvalidAPIKey.Status(),
			Code:    apierror.InvalidAPIKey.String(),
			Message: "authentication required",
			ErrorId: requestid,
		}, nil
	}

	// Get project
	slog.DebugContext(ctx, "getting project")
	project, err := env.Database.GetProjectByName(ctx, request.Name)
	if errors.Is(err, pgx.ErrNoRows) {
		slog.ErrorContext(ctx, "failed to get project", "error", err)
		return DeleteProjectsName404JSONResponse{
			Status:  apierror.ProjectNotFound.Status(),
			Code:    apierror.ProjectNotFound.String(),
			Message: "project not found",
			ErrorId: requestid,
		}, nil
	}
	if err != nil {
		slog.ErrorContext(ctx, "failed to get project", "error", err)
		return DeleteProjectsName500JSONResponse{
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
		return DeleteProjectsName500JSONResponse{
			Status:  apierror.InternalServerError.Status(),
			Code:    apierror.InternalServerError.String(),
			Message: "Internal Server Error",
			ErrorId: requestid,
		}, nil
	}
	if !authorized {
		slog.ErrorContext(ctx, "user does not have permissions")
		return DeleteProjectsName403JSONResponse{
			Status:  apierror.InsufficientPermissions.Status(),
			Code:    apierror.InsufficientPermissions.String(),
			Message: "user does not have permission to delete branch",
			ErrorId: requestid,
		}, nil
	}

	// Get project branches
	slog.DebugContext(ctx, "getting project branches")
	branches, err := env.Database.GetProjectBranches(ctx, project.ID)
	if err != nil {
		slog.ErrorContext(ctx, "failed to get project branches", "error", err)
		return DeleteProjectsName500JSONResponse{
			Status:  apierror.InternalServerError.Status(),
			Code:    apierror.InternalServerError.String(),
			Message: "Internal Server Error",
			ErrorId: requestid,
		}, nil
	}

	for _, branch := range branches {
		services, err := env.Database.GetServicesByProject(
			ctx,
			database.GetServicesByProjectParams{
				ProjectID:     project.ID,
				ProjectBranch: branch,
			})
		if err != nil {
			slog.DebugContext(ctx, "failed to get services", "error", err)
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
			err = kubernetes.DeleteDeployment(ctx, namespace, svc.ServiceName, env.Config)
			if err != nil {
				slog.ErrorContext(ctx, "failed to delete deployment", "error", err)
			}
			err = kubernetes.DeleteService(ctx, namespace, svc.ServiceName, env.Config)
			if err != nil {
				slog.ErrorContext(ctx, "failed to deleted service", "error", err)
			}
			if svc.Ingress.Valid {
				err = kubernetes.DeleteIngress(ctx, namespace, svc.Ingress.String, env.Config)
				if err != nil {
					slog.ErrorContext(ctx, "failed to delete ingress", "error", err)
				}
			}
			err = env.Database.DeleteServiceById(ctx, svc.ID)
			if err != nil {
				slog.ErrorContext(ctx, "failed to delete service from database", "error", err)
				return DeleteProjectsName500JSONResponse{
					Status:  apierror.InternalServerError.Status(),
					Code:    apierror.InternalServerError.String(),
					Message: "Internal Server Error",
					ErrorId: requestid,
				}, nil
			}
		}

		// Delete volumes
		ids, err := env.Database.GetUnusedVolumeIdentifiers(
			ctx,
			database.GetUnusedVolumeIdentifiersParams{
				ProjectID: project.ID, ProjectBranch: branch, ExcludeVolumes: nil,
			})
		if err != nil {
			slog.ErrorContext(ctx, "failed to get volumes", "error", err)
			return DeleteProjectsName500JSONResponse{
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
		err = env.Database.DeleteUnusedVolumes(
			ctx,
			database.DeleteUnusedVolumesParams{
				ProjectID: project.ID, ProjectBranch: branch, ExcludeVolumes: nil,
			})
		if err != nil {
			slog.ErrorContext(ctx, "failed to delete unused volumes", "error", err)
			return DeleteProjectsName500JSONResponse{
				Status:  apierror.InternalServerError.Status(),
				Code:    apierror.InternalServerError.String(),
				Message: "Internal Server Error",
				ErrorId: requestid,
			}, nil
		}
		err = kubernetes.DeleteNamespace(ctx, namespace, env.Config)
		if err != nil {
			slog.ErrorContext(ctx, "failed to delete namespace", "error", err)
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
		slog.ErrorContext(ctx, "failed to delete project", "error", err)
		return DeleteProjectsName500JSONResponse{
			Status:  apierror.InternalServerError.Status(),
			Code:    apierror.InternalServerError.String(),
			Message: "Internal Server Error",
			ErrorId: requestid,
		}, nil
	}

	return DeleteProjectsName204Response{}, nil
}

func (Server) PostProjectsNameMembers(
	ctx context.Context, request PostProjectsNameMembersRequestObject,
) (PostProjectsNameMembersResponseObject, error) {
	env := env.FromContext(ctx)
	requestid := fmt.Sprintf("%d", requestid.FromContext(ctx))
	user := database.UserFromContext(ctx)
	if user == nil {
		return PostProjectsNameMembers401JSONResponse{
			Status:  apierror.InvalidAPIKey.Status(),
			Code:    apierror.InvalidAPIKey.String(),
			Message: "authentication required",
			ErrorId: requestid,
		}, nil
	}

	// Get project
	project, err := env.Database.GetProjectByName(ctx, request.Name)
	if errors.Is(err, pgx.ErrNoRows) {
		return PostProjectsNameMembers404JSONResponse{
			Status:  apierror.ProjectNotFound.Status(),
			Code:    apierror.ProjectNotFound.String(),
			Message: "project not found",
			ErrorId: requestid,
		}, nil
	}
	if err != nil {
		slog.ErrorContext(ctx, "failed to get project", "error", err)
		return PostProjectsNameMembers500JSONResponse{
			Status:  apierror.InternalServerError.Status(),
			Code:    apierror.InternalServerError.String(),
			Message: "Internal Server Error",
			ErrorId: requestid,
		}, nil
	}

	// Check caller is in project
	authorized, err := env.Database.IsUserInProject(ctx, database.IsUserInProjectParams{
		UserID:    user.ID,
		ProjectID: project.ID,
	})
	if err != nil {
		slog.ErrorContext(ctx, "failed to check user permissions", "error", err)
		return PostProjectsNameMembers500JSONResponse{
			Status:  apierror.InternalServerError.Status(),
			Code:    apierror.InternalServerError.String(),
			Message: "Internal Server Error",
			ErrorId: requestid,
		}, nil
	}
	if !authorized {
		return PostProjectsNameMembers403JSONResponse{
			Status:  apierror.InsufficientPermissions.Status(),
			Code:    apierror.InsufficientPermissions.String(),
			Message: "user does not have permission to add members",
			ErrorId: requestid,
		}, nil
	}

	// Look up target user by username
	if request.Body == nil || request.Body.Username == "" {
		return PostProjectsNameMembers400JSONResponse{
			Status:  apierror.BadRequest.Status(),
			Code:    apierror.BadRequest.String(),
			Message: "username is required",
			ErrorId: requestid,
		}, nil
	}
	targetUser, err := env.Database.GetUserByUsername(ctx, request.Body.Username)
	if errors.Is(err, pgx.ErrNoRows) {
		return PostProjectsNameMembers404JSONResponse{
			Status:  apierror.UserNotFound.Status(),
			Code:    apierror.UserNotFound.String(),
			Message: "user not found",
			ErrorId: requestid,
		}, nil
	}
	if err != nil {
		slog.ErrorContext(ctx, "failed to get user by username", "error", err)
		return PostProjectsNameMembers500JSONResponse{
			Status:  apierror.InternalServerError.Status(),
			Code:    apierror.InternalServerError.String(),
			Message: "Internal Server Error",
			ErrorId: requestid,
		}, nil
	}

	// Check target user isn't already in project
	alreadyMember, err := env.Database.IsUserInProject(ctx, database.IsUserInProjectParams{
		UserID:    targetUser.ID,
		ProjectID: project.ID,
	})
	if err != nil {
		slog.ErrorContext(ctx, "failed to check membership", "error", err)
		return PostProjectsNameMembers500JSONResponse{
			Status:  apierror.InternalServerError.Status(),
			Code:    apierror.InternalServerError.String(),
			Message: "Internal Server Error",
			ErrorId: requestid,
		}, nil
	}
	if alreadyMember {
		return PostProjectsNameMembers400JSONResponse{
			Status:  apierror.BadRequest.Status(),
			Code:    apierror.BadRequest.String(),
			Message: "user is already a member of this project",
			ErrorId: requestid,
		}, nil
	}

	// Add user to project
	err = env.Database.AddUserToProject(ctx, database.AddUserToProjectParams{
		UserID:    targetUser.ID,
		ProjectID: project.ID,
	})
	if err != nil {
		slog.ErrorContext(ctx, "failed to add user to project", "error", err)
		return PostProjectsNameMembers500JSONResponse{
			Status:  apierror.InternalServerError.Status(),
			Code:    apierror.InternalServerError.String(),
			Message: "Internal Server Error",
			ErrorId: requestid,
		}, nil
	}

	return PostProjectsNameMembers201Response{}, nil
}

func (Server) GetProjectsNameSecrets(
	ctx context.Context, request GetProjectsNameSecretsRequestObject,
) (GetProjectsNameSecretsResponseObject, error) {
	env := env.FromContext(ctx)
	requestid := fmt.Sprintf("%d", requestid.FromContext(ctx))
	user := database.UserFromContext(ctx)
	if user == nil {
		return GetProjectsNameSecrets401JSONResponse{
			Status:  apierror.InvalidAPIKey.Status(),
			Code:    apierror.InvalidAPIKey.String(),
			Message: "authentication required",
			ErrorId: requestid,
		}, nil
	}

	// Get project
	slog.DebugContext(ctx, "getting project")
	project, err := env.Database.GetProjectByName(ctx, request.Name)
	if errors.Is(err, pgx.ErrNoRows) {
		slog.ErrorContext(ctx, "failed to get project", "error", err)
		return GetProjectsNameSecrets404JSONResponse{
			Status:  apierror.ProjectNotFound.Status(),
			Code:    apierror.ProjectNotFound.String(),
			Message: "project not found",
			ErrorId: requestid,
		}, nil
	}
	if err != nil {
		slog.ErrorContext(ctx, "failed to get project", "error", err)
		return GetProjectsNameSecrets500JSONResponse{
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
		return GetProjectsNameSecrets500JSONResponse{
			Status:  apierror.InternalServerError.Status(),
			Code:    apierror.InternalServerError.String(),
			Message: "Internal Server Error",
			ErrorId: requestid,
		}, nil
	}
	if !authorized {
		slog.ErrorContext(ctx, "user does not have permissions")
		return GetProjectsNameSecrets403JSONResponse{
			Status:  apierror.InsufficientPermissions.Status(),
			Code:    apierror.InsufficientPermissions.String(),
			Message: "user does not have permission to delete branch",
			ErrorId: requestid,
		}, nil
	}

	// Get secrets
	var res []byte
	slog.DebugContext(ctx, "getting secrets")
	if request.Params.Values != nil && *request.Params.Values {
		vals, err := kubernetes.GetSecretValues(ctx, project.Name, env.Config)
		if err != nil {
			slog.ErrorContext(ctx, "failed to get secret values", "error", err)
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
			slog.ErrorContext(ctx, "failed to marshal secret values", "error", err)
			return GetProjectsNameSecrets500JSONResponse{
				Status:  apierror.InternalServerError.Status(),
				Code:    apierror.InternalServerError.String(),
				Message: "Internal Server Error",
				ErrorId: requestid,
			}, nil
		}
	} else {
		names, err := kubernetes.ListSecretNames(ctx, project.Name, env.Config)
		if err != nil {
			slog.ErrorContext(ctx, "failed to get secret names", "error", err)
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
			slog.ErrorContext(ctx, "failed to marshal secret names", "error", err)
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
	if user == nil {
		return PutProjectsNameSecrets401JSONResponse{
			Status:  apierror.InvalidAPIKey.Status(),
			Code:    apierror.InvalidAPIKey.String(),
			Message: "authentication required",
			ErrorId: requestid,
		}, nil
	}

	// Get project
	slog.DebugContext(ctx, "getting project")
	project, err := env.Database.GetProjectByName(ctx, request.Name)
	if errors.Is(err, pgx.ErrNoRows) {
		slog.ErrorContext(ctx, "failed to get project", "error", err)
		return PutProjectsNameSecrets404JSONResponse{
			Status:  apierror.ProjectNotFound.Status(),
			Code:    apierror.ProjectNotFound.String(),
			Message: "project not found",
			ErrorId: requestid,
		}, nil
	}
	if err != nil {
		slog.ErrorContext(ctx, "failed to get project", "error", err)
		return PutProjectsNameSecrets500JSONResponse{
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
		return PutProjectsNameSecrets500JSONResponse{
			Status:  apierror.InternalServerError.Status(),
			Code:    apierror.InternalServerError.String(),
			Message: "Internal Server Error",
			ErrorId: requestid,
		}, nil
	}
	if !authorized {
		slog.ErrorContext(ctx, "user does not have permissions")
		return PutProjectsNameSecrets403JSONResponse{
			Status:  apierror.InsufficientPermissions.Status(),
			Code:    apierror.InsufficientPermissions.String(),
			Message: "user does not have permission to delete branch",
			ErrorId: requestid,
		}, nil
	}

	// Get project branches
	slog.DebugContext(ctx, "getting project branches")
	branches, err := env.Database.GetProjectBranches(ctx, project.ID)
	if err != nil {
		slog.ErrorContext(ctx, "failed to get project branches", "error", err)
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
		slog.DebugContext(ctx, "Updating secrets for namespace", "namespace", namespace)
		err = kubernetes.UpdateSecret(
			ctx, namespace, fmt.Sprintf("%s-env", project.Name), secrets, env.Config,
		)
		if err != nil {
			slog.ErrorContext(ctx, "failed to update secrets", "error", err)
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
