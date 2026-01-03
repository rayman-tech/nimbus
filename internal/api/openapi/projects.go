package openapi

import (
	"context"
	"fmt"
	"log/slog"

	apierror "nimbus/internal/api/error"
	"nimbus/internal/api/requestid"
	"nimbus/internal/database"
	"nimbus/internal/env"
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
	return DeleteProjectsName204Response{}, nil
}

func (Server) GetProjectsNameSecrets(
	ctx context.Context, request GetProjectsNameSecretsRequestObject,
) (GetProjectsNameSecretsResponseObject, error) {
	return GetProjectsNameSecrets200JSONResponse{}, nil
}

func (Server) PutProjectsNameSecrets(
	ctx context.Context, request PutProjectsNameSecretsRequestObject,
) (PutProjectsNameSecretsResponseObject, error) {
	return PutProjectsNameSecrets200Response{}, nil
}
