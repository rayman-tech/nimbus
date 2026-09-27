package imageupdate

import (
	"context"
	"log/slog"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

const (
	Label               = "nimbus.dev/image-auto-refresh"
	SourceAnnotation    = "nimbus.dev/image-source"
	ContainerAnnotation = "nimbus.dev/image-container"
	PreviousAnnotation  = "nimbus.dev/previous-image"
	UpdatedAnnotation   = "nimbus.dev/image-updated-at"
	ErrorAnnotation     = "nimbus.dev/image-update-error"
	maxBackoff          = 5 * time.Minute
)

// MetadataKeys are owned by Nimbus and removed when tracking is disabled.
var MetadataKeys = []string{
	SourceAnnotation, ContainerAnnotation, PreviousAnnotation, UpdatedAnnotation, ErrorAnnotation,
}

type lookup struct {
	image string
	err   error
	next  time.Time
	delay time.Duration
}

// Controller runs serial polls; Kubernetes resource versions guard updates
// against concurrent deploys, controller instances, and disable/delete actions.
type Controller struct {
	client   kubernetes.Interface
	resolve  ResolveFunc
	interval time.Duration
	lookups  map[string]lookup
}

func NewController(client kubernetes.Interface, resolve ResolveFunc, interval time.Duration) *Controller {
	return &Controller{client: client, resolve: resolve, interval: interval, lookups: make(map[string]lookup)}
}

func (c *Controller) Run(ctx context.Context) {
	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()
	for {
		if err := c.check(ctx, time.Now()); err != nil && ctx.Err() == nil {
			slog.ErrorContext(ctx, "checking followed images", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (c *Controller) check(ctx context.Context, now time.Time) error {
	const pollTimeout = 30 * time.Second
	ctx, cancel := context.WithTimeout(ctx, pollTimeout)
	defer cancel()
	deployments, err := c.client.AppsV1().Deployments("").List(ctx, metav1.ListOptions{LabelSelector: Label + "=true"})
	if err != nil {
		return err
	}
	active := make(map[string]bool)
	checked := make(map[string]bool)
	for i := range deployments.Items {
		dep := &deployments.Items[i]
		source := dep.Annotations[SourceAnnotation]
		active[source] = true
		if dep.DeletionTimestamp != nil || RolloutState(dep) != "ready" {
			continue
		}
		container := containerIndex(dep)
		if source == "" || container < 0 {
			continue
		}
		result, ok := c.lookups[source]
		if !checked[source] && (!ok || result.err == nil || !now.Before(result.next)) {
			image, lookupErr := c.resolve(ctx, source)
			delay := c.interval
			if lookupErr != nil {
				if result.err != nil {
					delay = min(result.delay*2, max(maxBackoff, c.interval))
				}
				slog.ErrorContext(ctx, "resolving followed image", "image", source, "error", lookupErr)
			}
			result = lookup{image: image, err: lookupErr, next: now.Add(delay), delay: delay}
			c.lookups[source] = result
		}
		checked[source] = true
		updated := dep.DeepCopy()
		if result.err != nil {
			const message = "Registry lookup failed; see Nimbus server logs."
			if updated.Annotations[ErrorAnnotation] == message {
				continue
			}
			updated.Annotations[ErrorAnnotation] = message
		} else {
			current := updated.Spec.Template.Spec.Containers[container].Image
			if current == result.image && updated.Annotations[ErrorAnnotation] == "" {
				continue
			}
			delete(updated.Annotations, ErrorAnnotation)
			if current != result.image {
				updated.Annotations[PreviousAnnotation] = current
				updated.Annotations[UpdatedAnnotation] = now.UTC().Format(time.RFC3339)
				updated.Spec.Template.Spec.Containers[container].Image = result.image
			}
		}
		// Update never creates a missing Deployment. Its resourceVersion and UID
		// prevent a stale poll from overwriting a new configuration or replacement.
		if _, err := c.client.AppsV1().Deployments(dep.Namespace).Update(ctx, updated, metav1.UpdateOptions{}); err != nil {
			slog.WarnContext(ctx, "updating followed image", "namespace", dep.Namespace, "service", dep.Name, "error", err)
			continue
		}
		if result.err == nil && dep.Spec.Template.Spec.Containers[container].Image != result.image {
			slog.InfoContext(ctx, "updated followed image", "namespace", dep.Namespace, "service", dep.Name, "image", result.image)
		}
	}
	for source := range c.lookups {
		if !active[source] {
			delete(c.lookups, source)
		}
	}
	return nil
}

func containerIndex(dep *appsv1.Deployment) int {
	for i, container := range dep.Spec.Template.Spec.Containers {
		if container.Name == dep.Annotations[ContainerAnnotation] {
			return i
		}
	}
	return -1
}

// RolloutState does not mistake a running old pod for a successful new rollout.
func RolloutState(dep *appsv1.Deployment) string {
	if dep.Spec.Paused {
		return "paused"
	}
	if dep.Status.ObservedGeneration < dep.Generation {
		return "updating"
	}
	for _, condition := range dep.Status.Conditions {
		if condition.Type == appsv1.DeploymentProgressing && condition.Reason == "ProgressDeadlineExceeded" {
			return "failed"
		}
	}
	desired := int32(1)
	if dep.Spec.Replicas != nil {
		desired = *dep.Spec.Replicas
	}
	if dep.Status.UpdatedReplicas != desired || dep.Status.Replicas != desired || dep.Status.AvailableReplicas != desired {
		return "updating"
	}
	return "ready"
}
