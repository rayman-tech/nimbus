package openapi

import "context"

func (Server) GetProjects(
	ctx context.Context, request GetProjectsRequestObject,
) (GetProjectsResponseObject, error) {
	return GetProjects200JSONResponse{}, nil
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
