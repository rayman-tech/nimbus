package openapi

import "context"

func (Server) GetServices(
	ctx context.Context, request GetServicesRequestObject,
) (GetServicesResponseObject, error) {
	return GetServices200JSONResponse{}, nil
}

func (Server) GetServicesName(
	ctx context.Context, request GetServicesNameRequestObject,
) (GetServicesNameResponseObject, error) {
	return GetServicesName200JSONResponse{}, nil
}

func (Server) GetServicesNameLogs(
	ctx context.Context, request GetServicesNameLogsRequestObject,
) (GetServicesNameLogsResponseObject, error) {
	return GetServicesNameLogs200TextResponse("OK"), nil
}
