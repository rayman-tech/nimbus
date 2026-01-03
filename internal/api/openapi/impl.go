package openapi

import (
	"bytes"
	"context"
	"strconv"

	"nimbus/docs"
	apiError "nimbus/internal/api/error"
	"nimbus/internal/api/requestid"
)

type Server struct{}

func NewServer() Server {
	return Server{}
}

func (Server) GetHealth(
	ctx context.Context, request GetHealthRequestObject,
) (GetHealthResponseObject, error) {
	return GetHealth204Response{}, nil
}

func (Server) GetOpenapiYaml(
	ctx context.Context, request GetOpenapiYamlRequestObject,
) (GetOpenapiYamlResponseObject, error) {
	requestID := strconv.FormatUint(requestid.FromContext(ctx), 10)

	data, err := docs.Docs.ReadFile("api.yaml")
	if err != nil {
		return GetOpenapiYaml500JSONResponse{
			Code:    apiError.InternalServerError.String(),
			Status:  apiError.InternalServerError.Status(),
			Message: "Internal Server Error",
			ErrorId: requestID,
		}, nil
	}

	return GetOpenapiYaml200ApplicationxYamlResponse{
		Body:          bytes.NewReader(data),
		ContentLength: int64(len(data)),
	}, nil
}
