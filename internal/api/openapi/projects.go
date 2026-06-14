package openapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

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
	rid := fmt.Sprintf("%d", requestid.FromContext(ctx))
	user := database.UserFromContext(ctx)
	if user == nil {
		return GetProjects401JSONResponse(authError(rid)), nil
	}

	dbProjects, err := env.Database.GetProjectsByUser(ctx, user.ID)
	if err != nil {
		slog.ErrorContext(ctx, "failed to get projects", "error", err)
		return GetProjects500JSONResponse(internalError(rid)), nil
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
	rid := fmt.Sprintf("%d", requestid.FromContext(ctx))
	user := database.UserFromContext(ctx)
	if user == nil {
		return PostProjects401JSONResponse(authError(rid)), nil
	}

	if request.Body == nil || request.Body.Name == "" {
		return PostProjects400JSONResponse{
			Status:  apierror.BadRequest.Status(),
			Code:    apierror.BadRequest.String(),
			Message: "project name is required",
			ErrorId: rid,
		}, nil
	}

	// Check if project already exists
	_, err := env.Database.GetProjectByName(ctx, request.Body.Name)
	if err == nil {
		return PostProjects400JSONResponse{
			Status:  apierror.BadRequest.Status(),
			Code:    apierror.BadRequest.String(),
			Message: "project already exists",
			ErrorId: rid,
		}, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		slog.ErrorContext(ctx, "failed to check project existence", "error", err)
		return PostProjects500JSONResponse(internalError(rid)), nil
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
		return PostProjects500JSONResponse(internalError(rid)), nil
	}

	// Add the creating user to the project
	err = env.Database.AddUserToProject(ctx, database.AddUserToProjectParams{
		UserID:    user.ID,
		ProjectID: project.ID,
	})
	if err != nil {
		slog.ErrorContext(ctx, "failed to add user to project", "error", err)
		return PostProjects500JSONResponse(internalError(rid)), nil
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
	rid := fmt.Sprintf("%d", requestid.FromContext(ctx))
	user := database.UserFromContext(ctx)
	if user == nil {
		return DeleteProjectsName401JSONResponse(authError(rid)), nil
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
			ErrorId: rid,
		}, nil
	}
	if err != nil {
		slog.ErrorContext(ctx, "failed to get project", "error", err)
		return DeleteProjectsName500JSONResponse(internalError(rid)), nil
	}

	// Check permissions
	slog.DebugContext(ctx, "getting user permissions")
	authorized, err := env.Database.IsUserInProject(ctx, database.IsUserInProjectParams{
		UserID:    user.ID,
		ProjectID: project.ID,
	})
	if err != nil {
		slog.ErrorContext(ctx, "failed to get user permissions", "error", err)
		return DeleteProjectsName500JSONResponse(internalError(rid)), nil
	}
	if !authorized {
		slog.ErrorContext(ctx, "user does not have permissions")
		return DeleteProjectsName403JSONResponse(forbiddenError(rid, "user does not have permission to delete project")), nil
	}

	// Get project branches
	slog.DebugContext(ctx, "getting project branches")
	branches, err := env.Database.GetProjectBranches(ctx, project.ID)
	if err != nil {
		slog.ErrorContext(ctx, "failed to get project branches", "error", err)
		return DeleteProjectsName500JSONResponse(internalError(rid)), nil
	}

	for _, branch := range branches {
		namespace := utils.GetSanitizedNamespace(project.Name, branch)
		if err := deleteBranchResources(ctx, namespace, project.ID, branch, env.Database, env.Config); err != nil {
			slog.ErrorContext(ctx, "failed to delete branch resources", "branch", branch, "error", err)
			return DeleteProjectsName500JSONResponse(internalError(rid)), nil
		}
	}

	err = env.Database.DeleteProject(ctx, project.ID)
	if err != nil {
		slog.ErrorContext(ctx, "failed to delete project", "error", err)
		return DeleteProjectsName500JSONResponse(internalError(rid)), nil
	}

	return DeleteProjectsName204Response{}, nil
}

func (Server) PostProjectsNameMembers(
	ctx context.Context, request PostProjectsNameMembersRequestObject,
) (PostProjectsNameMembersResponseObject, error) {
	env := env.FromContext(ctx)
	rid := fmt.Sprintf("%d", requestid.FromContext(ctx))
	user := database.UserFromContext(ctx)
	if user == nil {
		return PostProjectsNameMembers401JSONResponse(authError(rid)), nil
	}

	// Get project
	project, err := env.Database.GetProjectByName(ctx, request.Name)
	if errors.Is(err, pgx.ErrNoRows) {
		return PostProjectsNameMembers404JSONResponse{
			Status:  apierror.ProjectNotFound.Status(),
			Code:    apierror.ProjectNotFound.String(),
			Message: "project not found",
			ErrorId: rid,
		}, nil
	}
	if err != nil {
		slog.ErrorContext(ctx, "failed to get project", "error", err)
		return PostProjectsNameMembers500JSONResponse(internalError(rid)), nil
	}

	// Check caller is in project
	authorized, err := env.Database.IsUserInProject(ctx, database.IsUserInProjectParams{
		UserID:    user.ID,
		ProjectID: project.ID,
	})
	if err != nil {
		slog.ErrorContext(ctx, "failed to check user permissions", "error", err)
		return PostProjectsNameMembers500JSONResponse(internalError(rid)), nil
	}
	if !authorized {
		return PostProjectsNameMembers403JSONResponse(forbiddenError(rid, "user does not have permission to add members")), nil
	}

	// Look up target user by username
	if request.Body == nil || request.Body.Username == "" {
		return PostProjectsNameMembers400JSONResponse{
			Status:  apierror.BadRequest.Status(),
			Code:    apierror.BadRequest.String(),
			Message: "username is required",
			ErrorId: rid,
		}, nil
	}
	targetUser, err := env.Database.GetUserByUsername(ctx, request.Body.Username)
	if errors.Is(err, pgx.ErrNoRows) {
		return PostProjectsNameMembers404JSONResponse{
			Status:  apierror.UserNotFound.Status(),
			Code:    apierror.UserNotFound.String(),
			Message: "user not found",
			ErrorId: rid,
		}, nil
	}
	if err != nil {
		slog.ErrorContext(ctx, "failed to get user by username", "error", err)
		return PostProjectsNameMembers500JSONResponse(internalError(rid)), nil
	}

	// Check target user isn't already in project
	alreadyMember, err := env.Database.IsUserInProject(ctx, database.IsUserInProjectParams{
		UserID:    targetUser.ID,
		ProjectID: project.ID,
	})
	if err != nil {
		slog.ErrorContext(ctx, "failed to check membership", "error", err)
		return PostProjectsNameMembers500JSONResponse(internalError(rid)), nil
	}
	if alreadyMember {
		return PostProjectsNameMembers400JSONResponse{
			Status:  apierror.BadRequest.Status(),
			Code:    apierror.BadRequest.String(),
			Message: "user is already a member of this project",
			ErrorId: rid,
		}, nil
	}

	// Add user to project
	err = env.Database.AddUserToProject(ctx, database.AddUserToProjectParams{
		UserID:    targetUser.ID,
		ProjectID: project.ID,
	})
	if err != nil {
		slog.ErrorContext(ctx, "failed to add user to project", "error", err)
		return PostProjectsNameMembers500JSONResponse(internalError(rid)), nil
	}

	return PostProjectsNameMembers201Response{}, nil
}

func (Server) GetProjectsNameSecrets(
	ctx context.Context, request GetProjectsNameSecretsRequestObject,
) (GetProjectsNameSecretsResponseObject, error) {
	env := env.FromContext(ctx)
	rid := fmt.Sprintf("%d", requestid.FromContext(ctx))
	user := database.UserFromContext(ctx)
	if user == nil {
		return GetProjectsNameSecrets401JSONResponse(authError(rid)), nil
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
			ErrorId: rid,
		}, nil
	}
	if err != nil {
		slog.ErrorContext(ctx, "failed to get project", "error", err)
		return GetProjectsNameSecrets500JSONResponse(internalError(rid)), nil
	}

	// Check permissions
	slog.DebugContext(ctx, "getting user permissions")
	authorized, err := env.Database.IsUserInProject(ctx, database.IsUserInProjectParams{
		UserID:    user.ID,
		ProjectID: project.ID,
	})
	if err != nil {
		slog.ErrorContext(ctx, "failed to get user permissions", "error", err)
		return GetProjectsNameSecrets500JSONResponse(internalError(rid)), nil
	}
	if !authorized {
		slog.ErrorContext(ctx, "user does not have permissions")
		return GetProjectsNameSecrets403JSONResponse(forbiddenError(rid, "user does not have permission to view secrets")), nil
	}

	// Get secrets
	var res []byte
	slog.DebugContext(ctx, "getting secrets")
	if request.Params.Values != nil && *request.Params.Values {
		vals, err := kubernetes.GetSecretValues(ctx, project.Name, env.Config)
		if err != nil {
			slog.ErrorContext(ctx, "failed to get secret values", "error", err)
			return GetProjectsNameSecrets500JSONResponse(internalError(rid)), nil
		}
		if vals == nil {
			vals = make(map[string]string)
		}
		res, err = json.Marshal(SecretsValuesResponse{Secrets: &vals})
		if err != nil {
			slog.ErrorContext(ctx, "failed to marshal secret values", "error", err)
			return GetProjectsNameSecrets500JSONResponse(internalError(rid)), nil
		}
	} else {
		names, err := kubernetes.ListSecretNames(ctx, project.Name, env.Config)
		if err != nil {
			slog.ErrorContext(ctx, "failed to get secret names", "error", err)
			return GetProjectsNameSecrets500JSONResponse(internalError(rid)), nil
		}
		if names == nil {
			names = make([]string, 0)
		}
		res, err = json.Marshal(SecretsNamesResponse{Secrets: &names})
		if err != nil {
			slog.ErrorContext(ctx, "failed to marshal secret names", "error", err)
			return GetProjectsNameSecrets500JSONResponse(internalError(rid)), nil
		}
	}

	return GetProjectsNameSecrets200JSONResponse{union: res}, nil
}

func (Server) PutProjectsNameSecrets(
	ctx context.Context, request PutProjectsNameSecretsRequestObject,
) (PutProjectsNameSecretsResponseObject, error) {
	env := env.FromContext(ctx)
	rid := fmt.Sprintf("%d", requestid.FromContext(ctx))
	user := database.UserFromContext(ctx)
	if user == nil {
		return PutProjectsNameSecrets401JSONResponse(authError(rid)), nil
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
			ErrorId: rid,
		}, nil
	}
	if err != nil {
		slog.ErrorContext(ctx, "failed to get project", "error", err)
		return PutProjectsNameSecrets500JSONResponse(internalError(rid)), nil
	}

	// Check permissions
	slog.DebugContext(ctx, "getting user permissions")
	authorized, err := env.Database.IsUserInProject(ctx, database.IsUserInProjectParams{
		UserID:    user.ID,
		ProjectID: project.ID,
	})
	if err != nil {
		slog.ErrorContext(ctx, "failed to get user permissions", "error", err)
		return PutProjectsNameSecrets500JSONResponse(internalError(rid)), nil
	}
	if !authorized {
		slog.ErrorContext(ctx, "user does not have permissions")
		return PutProjectsNameSecrets403JSONResponse(forbiddenError(rid, "user does not have permission to update secrets")), nil
	}

	// Get project branches
	slog.DebugContext(ctx, "getting project branches")
	branches, err := env.Database.GetProjectBranches(ctx, project.ID)
	if err != nil {
		slog.ErrorContext(ctx, "failed to get project branches", "error", err)
		return PutProjectsNameSecrets500JSONResponse(internalError(rid)), nil
	}
	if len(branches) == 0 {
		branches = []string{utils.DefaultBranch}
	}
	hasMainBranch := false
	for _, b := range branches {
		if utils.IsMainBranch(b) {
			hasMainBranch = true
			break
		}
	}
	if !hasMainBranch {
		branches = append(branches, utils.DefaultBranch)
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
			return PutProjectsNameSecrets500JSONResponse(internalError(rid)), nil
		}
	}

	return PutProjectsNameSecrets200Response{}, nil
}
