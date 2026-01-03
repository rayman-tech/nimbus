package openapi

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"

	apierror "nimbus/internal/api/error"
	"nimbus/internal/api/requestid"
	"nimbus/internal/database"
	"nimbus/internal/env"
	"nimbus/internal/kubernetes"
	"nimbus/internal/utils"

	"github.com/jackc/pgx/v5"
	"github.com/oapi-codegen/nullable"
)

// StreamingLogsResponse implements GetServicesNameLogsResponseObject for streaming logs.
type StreamingLogsResponse struct {
	stream io.ReadCloser
}

// VisitGetServicesNameLogsResponse implements the visitor pattern to stream logs.
func (r StreamingLogsResponse) VisitGetServicesNameLogsResponse(w http.ResponseWriter) error {
	defer func() { _ = r.stream.Close() }()

	w.Header().Set("Content-Type", "text/plain")
	w.WriteHeader(http.StatusOK)

	const bufLen = 1024
	flusher, _ := w.(http.Flusher)
	buf := make([]byte, bufLen)

	for {
		n, err := r.stream.Read(buf)
		if n > 0 {
			_, writeErr := w.Write(buf[:n])
			if writeErr != nil {
				return writeErr
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
	}
}

func (Server) GetServices(
	ctx context.Context, request GetServicesRequestObject,
) (GetServicesResponseObject, error) {
	env := env.FromContext(ctx)
	requestid := fmt.Sprintf("%d", requestid.FromContext(ctx))
	user := database.UserFromContext(ctx)

	services, err := env.Database.GetServicesByUser(ctx, user.ID)
	if err != nil {
		env.Logger.ErrorContext(ctx, "failed to get services",
			slog.String("user_id", user.ID.String()),
			slog.Any("error", err))
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
		pods, err := kubernetes.GetPods(ctx, namespace, svc.ServiceName, env)
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
	env := env.FromContext(ctx)
	requestid := fmt.Sprintf("%d", requestid.FromContext(ctx))
	user := database.UserFromContext(ctx)

	var branch string
	if request.Params.Branch != nil {
		branch = *request.Params.Branch
	} else {
		branch = "main"
	}

	// Get project
	env.Logger.DebugContext(ctx, "get project",
		slog.String("name", request.Name))
	project, err := env.Database.GetProjectByName(ctx, request.Name)
	if errors.Is(err, pgx.ErrNoRows) {
		env.Logger.ErrorContext(ctx, "project not found",
			slog.String("name", request.Name),
			slog.Any("error", err))
		return GetServicesName404JSONResponse{
			Status:  apierror.ProjectNotFound.Status(),
			Code:    apierror.ProjectNotFound.String(),
			Message: "project not found",
			ErrorId: requestid,
		}, nil
	}
	if err != nil {
		env.Logger.ErrorContext(ctx, "failed to get project by name",
			slog.String("name", request.Name),
			slog.Any("error", err))
		return GetServicesName500JSONResponse{
			Status:  apierror.InternalServerError.Status(),
			Code:    apierror.InternalServerError.String(),
			Message: "Internal Server Error",
			ErrorId: requestid,
		}, nil
	}

	// Check permissions
	env.Logger.DebugContext(ctx, "check user permissions",
		slog.String("project", project.Name))
	authorized, err := env.Database.IsUserInProject(ctx, database.IsUserInProjectParams{
		UserID: user.ID,
	})
	if err != nil {
		env.Logger.ErrorContext(ctx, "failed to check user permissions",
			slog.String("project", project.Name),
			slog.Any("error", err))
		return GetServicesName500JSONResponse{
			Status:  apierror.InternalServerError.Status(),
			Code:    apierror.InternalServerError.String(),
			Message: "Internal Server Error",
			ErrorId: requestid,
		}, nil
	}
	if !authorized {
		env.Logger.ErrorContext(ctx, "insufficient permissions")
		return GetServicesName403JSONResponse{
			Status:  apierror.InsufficientPermissions.Status(),
			Code:    apierror.InsufficientPermissions.String(),
			Message: "user does not have permissions to view services",
			ErrorId: requestid,
		}, nil
	}

	// Get service
	env.Logger.DebugContext(ctx, "getting service",
		slog.String("service", request.Name),
		slog.String("project", project.Name))
	svc, err := env.Database.GetServiceByName(ctx, database.GetServiceByNameParams{
		ServiceName: request.Name,
		ProjectID:   project.ID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		env.Logger.ErrorContext(ctx, "service not found",
			slog.String("service", request.Name),
			slog.String("project", project.Name),
			slog.Any("error", err))
		return GetServicesName404JSONResponse{
			Status:  apierror.ServiceNotFound.Status(),
			Code:    apierror.ServiceNotFound.String(),
			Message: "service not found",
			ErrorId: requestid,
		}, nil
	}
	if err != nil {
		env.Logger.ErrorContext(ctx, "failed to get service",
			slog.String("service", request.Name),
			slog.String("project", project.Name),
			slog.Any("error", err))
		return GetServicesName500JSONResponse{
			Status:  apierror.InternalServerError.Status(),
			Code:    apierror.InternalServerError.String(),
			Message: "Internal Server Error",
			ErrorId: requestid,
		}, nil
	}

	// Get pods
	namespace := utils.GetSanitizedNamespace(project.Name, branch)
	pods, err := kubernetes.GetPods(ctx, namespace, request.Name, env)
	if err != nil {
		env.Logger.ErrorContext(ctx, "failed to get pods",
			slog.String("service", request.Name),
			slog.String("namespace", namespace),
			slog.Any("error", err))
		return GetServicesName500JSONResponse{
			Status:  apierror.InternalServerError.Status(),
			Code:    apierror.InternalServerError.String(),
			Message: "Internal Server Error",
			ErrorId: requestid,
		}, nil
	}

	var logs string
	const logLines = 20
	if len(pods) > 0 {
		data, err := kubernetes.GetPodLogsTail(ctx, namespace, pods[0].Name, logLines, env)
		if err != nil {
			env.Logger.ErrorContext(ctx, "failed to get pod logs",
				slog.String("service", request.Name),
				slog.String("pod", pods[0].Name),
				slog.Any("error", err))
			return GetServicesName500JSONResponse{
				Status:  apierror.InternalServerError.Status(),
				Code:    apierror.InternalServerError.String(),
				Message: "Internal Server Error",
				ErrorId: requestid,
			}, nil
		}
		logs = string(data)
	}

	// Create response
	podStatuses := make([]PodStatus, 0, len(pods))
	for _, pod := range pods {
		phase := PodStatusPhase(pod.Status.Phase)
		podStatuses = append(podStatuses, PodStatus{
			Name:  &pod.Name,
			Phase: &phase,
		})
	}

	res := GetServicesName200JSONResponse{
		Project:     &project.Name,
		Branch:      &branch,
		Name:        &request.Name,
		Logs:        &logs,
		PodStatuses: &podStatuses,
	}

	if svc.NodePorts == nil {
		ports := make([]int32, 0)
		res.NodePorts = &ports
	} else {
		res.NodePorts = &svc.NodePorts
	}

	if svc.Ingress.Valid {
		res.Ingress = nullable.NewNullableWithValue(svc.Ingress.String)
	}

	return res, nil
}

func (Server) GetServicesNameLogs(
	ctx context.Context, request GetServicesNameLogsRequestObject,
) (GetServicesNameLogsResponseObject, error) {
	env := env.FromContext(ctx)
	requestid := fmt.Sprintf("%d", requestid.FromContext(ctx))
	user := database.UserFromContext(ctx)

	var branch string
	if request.Params.Branch != nil {
		branch = *request.Params.Branch
	} else {
		branch = "main"
	}

	// Get project
	env.Logger.DebugContext(ctx, "get project",
		slog.String("name", request.Name))
	project, err := env.Database.GetProjectByName(ctx, request.Name)
	if errors.Is(err, pgx.ErrNoRows) {
		env.Logger.ErrorContext(ctx, "project not found",
			slog.String("name", request.Name),
			slog.Any("error", err))
		return GetServicesNameLogs404JSONResponse{
			Status:  apierror.ProjectNotFound.Status(),
			Code:    apierror.ProjectNotFound.String(),
			Message: "project not found",
			ErrorId: requestid,
		}, nil
	}
	if err != nil {
		env.Logger.ErrorContext(ctx, "failed to get project by name",
			slog.String("name", request.Name),
			slog.Any("error", err))
		return GetServicesNameLogs500JSONResponse{
			Status:  apierror.InternalServerError.Status(),
			Code:    apierror.InternalServerError.String(),
			Message: "Internal Server Error",
			ErrorId: requestid,
		}, nil
	}

	// Check permissions
	env.Logger.DebugContext(ctx, "check user permissions",
		slog.String("project", project.Name))
	authorized, err := env.Database.IsUserInProject(ctx, database.IsUserInProjectParams{
		UserID: user.ID,
	})
	if err != nil {
		env.Logger.ErrorContext(ctx, "failed to check user permissions",
			slog.String("project", project.Name),
			slog.Any("error", err))
		return GetServicesNameLogs500JSONResponse{
			Status:  apierror.InternalServerError.Status(),
			Code:    apierror.InternalServerError.String(),
			Message: "Internal Server Error",
			ErrorId: requestid,
		}, nil
	}
	if !authorized {
		env.Logger.ErrorContext(ctx, "insufficient permissions",
			slog.String("project", project.Name))
		return GetServicesNameLogs403JSONResponse{
			Status:  apierror.InsufficientPermissions.Status(),
			Code:    apierror.InsufficientPermissions.String(),
			Message: "user does not have permissions to view services",
			ErrorId: requestid,
		}, nil
	}

	// Stream logs
	namespace := utils.GetSanitizedNamespace(project.Name, branch)
	stream, err := kubernetes.StreamServiceLogs(ctx, namespace, request.Name, env)
	if err != nil {
		env.Logger.ErrorContext(ctx, "failed to stream logs",
			slog.String("service", request.Name),
			slog.String("namespace", namespace),
			slog.Any("error", err))
		return GetServicesNameLogs500JSONResponse{
			Status:  apierror.InternalServerError.Status(),
			Code:    apierror.InternalServerError.String(),
			Message: "Internal Server Error",
			ErrorId: requestid,
		}, nil
	}

	return StreamingLogsResponse{stream: stream}, nil
}
