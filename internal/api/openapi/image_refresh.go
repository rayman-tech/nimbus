package openapi

import (
	"context"
	"log/slog"

	"nimbus/internal/imageupdate"
	"nimbus/internal/kubernetes"

	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/api/errors"
)

func getImageRefreshStatus(ctx context.Context, namespace, service string) *ImageRefreshStatus {
	dep, err := kubernetes.GetDeployment(ctx, namespace, service)
	if err != nil {
		if !errors.IsNotFound(err) {
			slog.WarnContext(ctx, "reading image refresh status", "namespace", namespace, "service", service, "error", err)
		}
		return nil
	}
	return imageRefreshStatus(dep)
}

func imageRefreshStatus(dep *appsv1.Deployment) *ImageRefreshStatus {
	if dep.Labels[imageupdate.Label] != "true" {
		return nil
	}
	status := &ImageRefreshStatus{
		Source: dep.Annotations[imageupdate.SourceAnnotation],
		State:  ImageRefreshStatusState(imageupdate.RolloutState(dep)),
	}
	for _, container := range dep.Spec.Template.Spec.Containers {
		if container.Name == dep.Annotations[imageupdate.ContainerAnnotation] {
			status.Image = container.Image
		}
	}
	if value := dep.Annotations[imageupdate.PreviousAnnotation]; value != "" {
		status.PreviousImage = &value
	}
	if value := dep.Annotations[imageupdate.UpdatedAnnotation]; value != "" {
		status.UpdatedAt = &value
	}
	if value := dep.Annotations[imageupdate.ErrorAnnotation]; value != "" {
		status.LastError = &value
	}
	return status
}
