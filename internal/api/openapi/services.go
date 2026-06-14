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
	rid := fmt.Sprintf("%d", requestid.FromContext(ctx))
	user := database.UserFromContext(ctx)
	if user == nil {
		return GetServices401JSONResponse(authError(rid)), nil
	}

	services, err := env.Database.GetServicesByUser(ctx, user.ID)
	if err != nil {
		slog.ErrorContext(ctx, "failed to get services",
			"user_id", user.ID.String(),
			"error", err)
		return GetServices500JSONResponse(internalError(rid)), nil
	}

	items := make([]ServiceListItem, 0, len(services))
	for _, svc := range services {
		namespace := utils.GetSanitizedNamespace(svc.ProjectName, svc.ProjectBranch)
		pods, err := kubernetes.GetPods(ctx, namespace, svc.ServiceName)
		var status ServiceListItemStatus
		if err == nil && len(pods) > 0 {
			status = ServiceListItemStatus(pods[0].Status.Phase)
		} else {
			status = ServiceListItemStatusUnknown
		}
		item := ServiceListItem{
			Project: &svc.ProjectName,
			Branch:  &svc.ProjectBranch,
			Name:    &svc.ServiceName,
			Status:  &status,
		}
		if svc.CommitHash.Valid {
			item.CommitHash = nullable.NewNullableWithValue(svc.CommitHash.String)
		}
		items = append(items, item)
	}

	return GetServices200JSONResponse{
		Services: &items,
	}, nil
}

func (Server) GetServicesName(
	ctx context.Context, request GetServicesNameRequestObject,
) (GetServicesNameResponseObject, error) {
	env := env.FromContext(ctx)
	rid := fmt.Sprintf("%d", requestid.FromContext(ctx))
	user := database.UserFromContext(ctx)
	if user == nil {
		return GetServicesName401JSONResponse(authError(rid)), nil
	}

	var branch string
	if request.Params.Branch != nil {
		branch = *request.Params.Branch
	} else {
		branch = utils.DefaultBranch
	}

	// Get project
	slog.DebugContext(ctx, "get project",
		"name", request.Params.Project)
	project, err := env.Database.GetProjectByName(ctx, request.Params.Project)
	if errors.Is(err, pgx.ErrNoRows) {
		slog.ErrorContext(ctx, "project not found",
			"name", request.Params.Project,
			"error", err)
		return GetServicesName404JSONResponse{
			Status:  apierror.ProjectNotFound.Status(),
			Code:    apierror.ProjectNotFound.String(),
			Message: "project not found",
			ErrorId: rid,
		}, nil
	}
	if err != nil {
		slog.ErrorContext(ctx, "failed to get project by name",
			"name", request.Params.Project,
			"error", err)
		return GetServicesName500JSONResponse(internalError(rid)), nil
	}

	// Check permissions
	slog.DebugContext(ctx, "check user permissions",
		"project", project.Name)
	authorized, err := env.Database.IsUserInProject(ctx, database.IsUserInProjectParams{
		UserID:    user.ID,
		ProjectID: project.ID,
	})
	if err != nil {
		slog.ErrorContext(ctx, "failed to check user permissions",
			"project", project.Name,
			"error", err)
		return GetServicesName500JSONResponse(internalError(rid)), nil
	}
	if !authorized {
		slog.ErrorContext(ctx, "insufficient permissions")
		return GetServicesName403JSONResponse(forbiddenError(rid, "user does not have permissions to view services")), nil
	}

	// Get service
	slog.DebugContext(ctx, "getting service",
		"service", request.Name,
		"project", project.Name)
	svc, err := env.Database.GetServiceByName(ctx, database.GetServiceByNameParams{
		ServiceName:   request.Name,
		ProjectID:     project.ID,
		ProjectBranch: branch,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		slog.ErrorContext(ctx, "service not found",
			"service", request.Name,
			"project", project.Name,
			"error", err)
		return GetServicesName404JSONResponse{
			Status:  apierror.ServiceNotFound.Status(),
			Code:    apierror.ServiceNotFound.String(),
			Message: "service not found",
			ErrorId: rid,
		}, nil
	}
	if err != nil {
		slog.ErrorContext(ctx, "failed to get service",
			"service", request.Name,
			"project", project.Name,
			"error", err)
		return GetServicesName500JSONResponse(internalError(rid)), nil
	}

	// Get pods
	namespace := utils.GetSanitizedNamespace(project.Name, branch)
	pods, err := kubernetes.GetPods(ctx, namespace, request.Name)
	if err != nil {
		slog.ErrorContext(ctx, "failed to get pods",
			"service", request.Name,
			"namespace", namespace,
			"error", err)
		return GetServicesName500JSONResponse(internalError(rid)), nil
	}

	var logs string
	const logLines = 20
	if len(pods) > 0 {
		data, err := kubernetes.GetPodLogsTail(ctx, namespace, pods[0].Name, logLines)
		if err != nil {
			slog.ErrorContext(ctx, "failed to get pod logs",
				"service", request.Name,
				"pod", pods[0].Name,
				"error", err)
			return GetServicesName500JSONResponse(internalError(rid)), nil
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

	if svc.CommitHash.Valid {
		res.CommitHash = nullable.NewNullableWithValue(svc.CommitHash.String)
	}

	return res, nil
}

func (Server) GetServicesNameLogs(
	ctx context.Context, request GetServicesNameLogsRequestObject,
) (GetServicesNameLogsResponseObject, error) {
	env := env.FromContext(ctx)
	rid := fmt.Sprintf("%d", requestid.FromContext(ctx))
	user := database.UserFromContext(ctx)
	if user == nil {
		return GetServicesNameLogs401JSONResponse(authError(rid)), nil
	}

	var branch string
	if request.Params.Branch != nil {
		branch = *request.Params.Branch
	} else {
		branch = utils.DefaultBranch
	}

	// Get project
	slog.DebugContext(ctx, "get project",
		"name", request.Params.Project)
	project, err := env.Database.GetProjectByName(ctx, request.Params.Project)
	if errors.Is(err, pgx.ErrNoRows) {
		slog.ErrorContext(ctx, "project not found",
			"name", request.Params.Project,
			"error", err)
		return GetServicesNameLogs404JSONResponse{
			Status:  apierror.ProjectNotFound.Status(),
			Code:    apierror.ProjectNotFound.String(),
			Message: "project not found",
			ErrorId: rid,
		}, nil
	}
	if err != nil {
		slog.ErrorContext(ctx, "failed to get project by name",
			"name", request.Params.Project,
			"error", err)
		return GetServicesNameLogs500JSONResponse(internalError(rid)), nil
	}

	// Check permissions
	slog.DebugContext(ctx, "check user permissions",
		"project", project.Name)
	authorized, err := env.Database.IsUserInProject(ctx, database.IsUserInProjectParams{
		UserID:    user.ID,
		ProjectID: project.ID,
	})
	if err != nil {
		slog.ErrorContext(ctx, "failed to check user permissions",
			"project", project.Name,
			"error", err)
		return GetServicesNameLogs500JSONResponse(internalError(rid)), nil
	}
	if !authorized {
		slog.ErrorContext(ctx, "insufficient permissions",
			"project", project.Name)
		return GetServicesNameLogs403JSONResponse(forbiddenError(rid, "user does not have permissions to view services")), nil
	}

	// Stream logs
	namespace := utils.GetSanitizedNamespace(project.Name, branch)
	stream, err := kubernetes.StreamServiceLogs(ctx, namespace, request.Name)
	if err != nil {
		slog.ErrorContext(ctx, "failed to stream logs",
			"service", request.Name,
			"namespace", namespace,
			"error", err)
		return GetServicesNameLogs500JSONResponse(internalError(rid)), nil
	}

	return StreamingLogsResponse{stream: stream}, nil
}
