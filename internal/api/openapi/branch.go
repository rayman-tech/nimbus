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
	"nimbus/internal/utils"

	"github.com/jackc/pgx/v5"
)

func (Server) DeleteBranch(ctx context.Context, request DeleteBranchRequestObject) (DeleteBranchResponseObject, error) {
	env := env.FromContext(ctx)
	rid := fmt.Sprintf("%d", requestid.FromContext(ctx))
	user := database.UserFromContext(ctx)
	if user == nil {
		return DeleteBranch401JSONResponse(authError(rid)), nil
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
			ErrorId: rid,
		}, nil
	}
	if err != nil {
		slog.ErrorContext(ctx, "failed to get project", "error", err)
		return DeleteBranch500JSONResponse(internalError(rid)), nil
	}

	// Check permissions
	slog.DebugContext(ctx, "getting user permissions")
	authorized, err := env.Database.IsUserInProject(ctx, database.IsUserInProjectParams{
		UserID:    user.ID,
		ProjectID: project.ID,
	})
	if err != nil {
		slog.ErrorContext(ctx, "failed to get user permissions", "error", err)
		return DeleteBranch500JSONResponse(internalError(rid)), nil
	}
	if !authorized {
		slog.ErrorContext(ctx, "user does not have permissions")
		return DeleteBranch403JSONResponse(forbiddenError(rid, "user does not have permission to delete branch")), nil
	}

	// Delete branch resources
	slog.DebugContext(ctx, "deleting branch resources")
	namespace := utils.GetSanitizedNamespace(project.Name, request.Params.Branch)
	if err := deleteBranchResources(ctx, namespace, project.ID, request.Params.Branch, env.Database, env.Config); err != nil {
		slog.ErrorContext(ctx, "failed to delete branch resources", "error", err)
		return DeleteBranch500JSONResponse(internalError(rid)), nil
	}

	return DeleteBranch204Response{}, nil
}
