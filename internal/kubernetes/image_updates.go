package kubernetes

import (
	"context"
	"time"

	"nimbus/internal/imageupdate"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func RunImageUpdates(ctx context.Context, interval time.Duration) {
	imageupdate.NewController(getClient(), imageupdate.Resolve, interval).Run(ctx)
}

func GetDeployment(ctx context.Context, namespace, name string) (*appsv1.Deployment, error) {
	return getClient().AppsV1().Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
}

func setImageTracking(deployment *appsv1.Deployment, source, container string) {
	if deployment.Labels == nil {
		deployment.Labels = make(map[string]string)
	}
	if deployment.Annotations == nil {
		deployment.Annotations = make(map[string]string)
	}
	delete(deployment.Labels, imageupdate.Label)
	for _, key := range imageupdate.MetadataKeys {
		delete(deployment.Annotations, key)
	}
	if source != "" {
		deployment.Labels[imageupdate.Label] = "true"
		deployment.Annotations[imageupdate.SourceAnnotation] = source
		deployment.Annotations[imageupdate.ContainerAnnotation] = container
	}
}
