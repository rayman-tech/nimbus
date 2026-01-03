package openapi

import (
	"context"
	"fmt"
	"log/slog"

	apierror "nimbus/internal/api/error"
	"nimbus/internal/api/requestid"
	"nimbus/internal/database"
	"nimbus/internal/env"
	"nimbus/internal/kubernetes"
	"nimbus/internal/utils"
)

func (Server) GetServices(
	ctx context.Context, request GetServicesRequestObject,
) (GetServicesResponseObject, error) {
	env := env.FromContext(ctx)
	requestid := fmt.Sprintf("%d", requestid.FromContext(ctx))
	user := database.UserFromContext(ctx)

	services, err := env.Database.GetServicesByUser(ctx, user.ID)
	if err != nil {
		env.Logger.ErrorContext(ctx, "failed to get services", slog.Any("error", err))
		return GetServices500JSONResponse{
			Status:  apierror.InternalServerError.Status(),
			Code:    apierror.InternalServerError.String(),
			Message: "Internal Server Error",
			ErrorId: requestid,
		}, nil
	}

	items := make([]ServiceListItem, 0, len(services))
	for _, svc := range services {
		namespace := utils.GetSanitizedNamespace(svc.ProjectName, svc.ProjectBranch)
		pods, err := kubernetes.GetPods(namespace, svc.ServiceName, env)
		var status ServiceListItemStatus
		if err == nil && len(pods) > 0 {
			status = ServiceListItemStatus(pods[0].Status.Phase)
		} else {
			status = ServiceListItemStatusUnknown
		}
		items = append(items, ServiceListItem{
			Project: &svc.ProjectName,
			Branch:  &svc.ProjectBranch,
			Name:    &svc.ServiceName,
			Status:  &status,
		})
	}

	return GetServices200JSONResponse{
		Services: &items,
	}, nil
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
