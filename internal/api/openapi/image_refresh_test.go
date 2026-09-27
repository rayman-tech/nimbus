package openapi

import (
	"testing"

	"nimbus/internal/imageupdate"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestImageRefreshStatusReportsDesiredImageAndRollout(t *testing.T) {
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Generation: 2,
			Labels: map[string]string{imageupdate.Label: "true"},
			Annotations: map[string]string{
				imageupdate.SourceAnnotation:    "registry.example.com/app:latest",
				imageupdate.ContainerAnnotation: "app",
				imageupdate.PreviousAnnotation:  "previous@sha256:abc",
				imageupdate.ErrorAnnotation:     "Registry lookup failed; see Nimbus server logs.",
			}},
		Spec: appsv1.DeploymentSpec{Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Name: "sidecar", Image: "other"}, {Name: "app", Image: "desired@sha256:def"}},
		}}},
		Status: appsv1.DeploymentStatus{ObservedGeneration: 1, AvailableReplicas: 1},
	}
	status := imageRefreshStatus(dep)
	if status.Image != "desired@sha256:def" || status.State != ImageRefreshStatusStateUpdating {
		t.Fatalf("old available replica must not report new rollout ready: %+v", status)
	}
	if status.LastError == nil || status.PreviousImage == nil || status.Source != dep.Annotations[imageupdate.SourceAnnotation] {
		t.Fatalf("missing tracking status: %+v", status)
	}
	delete(dep.Labels, imageupdate.Label)
	if imageRefreshStatus(dep) != nil {
		t.Fatal("non-opted-in service exposes tracking status")
	}
}
