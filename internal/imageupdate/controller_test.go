package imageupdate

import (
	"context"
	"fmt"
	"reflect"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes/fake"
	ktesting "k8s.io/client-go/testing"
)

const testInterval = 5 * time.Second

func followedDeployment() *appsv1.Deployment {
	replicas := int32(1)
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "app", Namespace: "project", UID: "first", ResourceVersion: "1", Generation: 1,
			Labels:      map[string]string{Label: "true"},
			Annotations: map[string]string{SourceAnnotation: "registry.example.com/app:latest", ContainerAnnotation: "app"}},
		Spec: appsv1.DeploymentSpec{Replicas: &replicas, Strategy: appsv1.DeploymentStrategy{Type: appsv1.RecreateDeploymentStrategyType},
			Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{Containers: []corev1.Container{
				{Name: "sidecar", Image: "sidecar:fixed"},
				{Name: "app", Image: oldImage, Env: []corev1.EnvVar{{Name: "KEEP", Value: "yes"}}},
			}, Volumes: []corev1.Volume{{Name: "ledger"}}}}},
		Status: appsv1.DeploymentStatus{ObservedGeneration: 1, Replicas: 1, UpdatedReplicas: 1, AvailableReplicas: 1},
	}
}

func TestChangedImageOnlyUpdatesTargetContainer(t *testing.T) {
	before := followedDeployment()
	client := fake.NewSimpleClientset(before)
	c := NewController(client, func(context.Context, string) (string, error) { return newImage, nil }, testInterval)
	now := time.Now()
	if err := c.check(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	after, _ := client.AppsV1().Deployments(before.Namespace).Get(context.Background(), before.Name, metav1.GetOptions{})
	wantSpec := before.Spec.DeepCopy()
	wantSpec.Template.Spec.Containers[1].Image = newImage
	if !reflect.DeepEqual(*wantSpec, after.Spec) {
		t.Fatal("update changed fields beyond target image")
	}
	if after.Annotations[PreviousAnnotation] != oldImage || after.Annotations[UpdatedAnnotation] == "" {
		t.Fatal("missing update history")
	}
	client.ClearActions()
	// A restarted controller learns the deployed digest from Kubernetes.
	c = NewController(client, func(context.Context, string) (string, error) { return newImage, nil }, testInterval)
	if err := c.check(context.Background(), now.Add(testInterval)); err != nil {
		t.Fatal(err)
	}
	for _, action := range client.Actions() {
		if action.GetVerb() != "list" {
			t.Fatalf("unchanged digest caused %s", action.GetVerb())
		}
	}
}

func TestPollingDeduplicatesAndBacksOff(t *testing.T) {
	first := followedDeployment()
	second := first.DeepCopy()
	second.Name, second.Namespace = "other", "preview"
	client := fake.NewSimpleClientset(first, second)
	calls, failing := 0, true
	c := NewController(client, func(context.Context, string) (string, error) {
		calls++
		if failing {
			return "", fmt.Errorf("registry unavailable")
		}
		return newImage, nil
	}, testInterval)
	now := time.Now()
	for _, offset := range []time.Duration{0, testInterval, 2 * testInterval} {
		if err := c.check(context.Background(), now.Add(offset)); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 2 {
		t.Fatalf("expected shared lookup with backoff, got %d calls", calls)
	}
	for _, d := range []*appsv1.Deployment{first, second} {
		got, _ := client.AppsV1().Deployments(d.Namespace).Get(context.Background(), d.Name, metav1.GetOptions{})
		if got.Spec.Template.Spec.Containers[1].Image != oldImage || got.Annotations[ErrorAnnotation] == "" {
			t.Fatal("lookup error changed workload or was not recorded")
		}
	}
	failing = false
	if err := c.check(context.Background(), now.Add(3*testInterval)); err != nil {
		t.Fatal(err)
	}
	if calls != 3 {
		t.Fatalf("recovery lookups = %d", calls)
	}
	for _, d := range []*appsv1.Deployment{first, second} {
		got, _ := client.AppsV1().Deployments(d.Namespace).Get(context.Background(), d.Name, metav1.GetOptions{})
		if got.Spec.Template.Spec.Containers[1].Image != newImage || got.Annotations[ErrorAnnotation] != "" {
			t.Fatal("lookup did not recover")
		}
	}
}

func TestSkipsDisabledPausedDeletingAndIncompleteRollouts(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*appsv1.Deployment)
	}{
		{"disabled", func(d *appsv1.Deployment) { delete(d.Labels, Label) }},
		{"paused", func(d *appsv1.Deployment) { d.Spec.Paused = true }},
		{"deleting", func(d *appsv1.Deployment) { now := metav1.Now(); d.DeletionTimestamp = &now }},
		{"unobserved generation", func(d *appsv1.Deployment) { d.Generation++ }},
		{"old replicas running", func(d *appsv1.Deployment) { d.Status.UpdatedReplicas = 0 }},
		{"not available", func(d *appsv1.Deployment) { d.Status.AvailableReplicas = 0 }},
		{"failed rollout", func(d *appsv1.Deployment) {
			d.Status.Conditions = []appsv1.DeploymentCondition{{Type: appsv1.DeploymentProgressing, Status: corev1.ConditionFalse, Reason: "ProgressDeadlineExceeded"}}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dep := followedDeployment()
			tc.change(dep)
			client := fake.NewSimpleClientset(dep)
			c := NewController(client, func(context.Context, string) (string, error) { t.Fatal("unexpected registry lookup"); return "", nil }, testInterval)
			if err := c.check(context.Background(), time.Now()); err != nil {
				t.Fatal(err)
			}
			for _, action := range client.Actions() {
				if action.GetVerb() != "list" {
					t.Fatal("unexpected Kubernetes write")
				}
			}
		})
	}
}

func TestConcurrentDeployOrDeleteWins(t *testing.T) {
	for _, deleting := range []bool{false, true} {
		t.Run(fmt.Sprintf("delete=%v", deleting), func(t *testing.T) {
			dep := followedDeployment()
			client := fake.NewSimpleClientset(dep)
			gvr := schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}
			// Fake clients do not implement resourceVersion conflicts; enforce the
			// API contract here and verify the stale poll supplies the old version.
			client.PrependReactor("update", "deployments", func(a ktesting.Action) (bool, runtime.Object, error) {
				obj := a.(ktesting.UpdateAction).GetObject().(*appsv1.Deployment)
				if obj.ResourceVersion != "1" || obj.UID != "first" {
					t.Fatal("lost concurrency preconditions")
				}
				return true, nil, apierrors.NewConflict(gvr.GroupResource(), dep.Name, fmt.Errorf("changed concurrently"))
			})
			c := NewController(client, func(context.Context, string) (string, error) {
				if deleting {
					if err := client.Tracker().Delete(gvr, dep.Namespace, dep.Name); err != nil {
						t.Fatal(err)
					}
				} else {
					changed := dep.DeepCopy()
					changed.ResourceVersion = "2"
					delete(changed.Labels, Label)
					changed.Spec.Template.Spec.Containers[1].Image = "manual:fixed"
					if err := client.Tracker().Update(gvr, changed, dep.Namespace); err != nil {
						t.Fatal(err)
					}
				}
				return newImage, nil
			}, testInterval)
			if err := c.check(context.Background(), time.Now()); err != nil {
				t.Fatal(err)
			}
			got, err := client.AppsV1().Deployments(dep.Namespace).Get(context.Background(), dep.Name, metav1.GetOptions{})
			if deleting {
				if !apierrors.IsNotFound(err) {
					t.Fatal("deleted deployment was recreated")
				}
				return
			}
			if err != nil || got.Spec.Template.Spec.Containers[1].Image != "manual:fixed" {
				t.Fatal("manual deploy overwritten")
			}
		})
	}
}

func TestConfiguredPollingIntervalAndShutdown(t *testing.T) {
	client := fake.NewSimpleClientset(followedDeployment())
	calls := make(chan struct{}, 10)
	c := NewController(client, func(context.Context, string) (string, error) {
		calls <- struct{}{}
		return oldImage, nil
	}, 5*time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { defer close(done); c.Run(ctx) }()
	for range 2 {
		select {
		case <-calls:
		case <-time.After(time.Second):
			t.Fatal("worker did not use configured interval")
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("worker did not shut down")
	}
}

func TestSuccessIsCheckedOnEveryPollDespiteClockJitter(t *testing.T) {
	client := fake.NewSimpleClientset(followedDeployment())
	calls := 0
	c := NewController(client, func(context.Context, string) (string, error) { calls++; return oldImage, nil }, testInterval)
	now := time.Now()
	if err := c.check(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	if err := c.check(context.Background(), now.Add(testInterval-time.Nanosecond)); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("worker skipped a polling tick: %d lookups", calls)
	}
}
