package openapi

import "context"

func (Server) PostDeploy(ctx context.Context, request PostDeployRequestObject) (PostDeployResponseObject, error) {
	return PostDeploy200JSONResponse{}, nil
}
