package kubernetes

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/util/retry"
)

// DeleteBranchStorage deletes claims and the branch namespace, then waits for
// reclamation. Only the provisioner deletes PVs and their storage. Production
// retains its configured reclaim policy; preview PVs must already use Delete.
// Claim references also find PVs left behind by an interrupted cleanup attempt.
func DeleteBranchStorage(ctx context.Context, namespace string, names []string, preview bool, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	claims := getClient().CoreV1().PersistentVolumeClaims(namespace)
	pvcs, err := claims.List(ctx, metav1.ListOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("listing PVCs: %w", err)
	}
	existing := map[string]corev1.PersistentVolumeClaim{}
	wanted := map[string]bool{}
	for _, name := range names {
		wanted[name] = true
	}
	if pvcs != nil {
		for _, pvc := range pvcs.Items {
			existing[pvc.Name] = pvc
			wanted[pvc.Name] = true
		}
	}
	// Do not destroy a retained preview's claim before reporting the policy error.
	if preview {
		volumes, err := getClient().CoreV1().PersistentVolumes().List(ctx, metav1.ListOptions{})
		if err != nil {
			return fmt.Errorf("listing preview PVs: %w", err)
		}
		for _, pv := range volumes.Items {
			ref := pv.Spec.ClaimRef
			if ref == nil || ref.Namespace != namespace {
				continue
			}
			if pv.Spec.PersistentVolumeReclaimPolicy != corev1.PersistentVolumeReclaimDelete {
				return fmt.Errorf("preview PV %s uses %s: explicitly migrate this preview PV to Delete before retrying cleanup", pv.Name, pv.Spec.PersistentVolumeReclaimPolicy)
			}
			if pvc, ok := existing[ref.Name]; ok && ref.UID != pvc.UID {
				return fmt.Errorf("preview PV %s claim UID does not match PVC %s", pv.Name, ref.Name)
			}
		}
	}
	for name := range wanted {
		options := metav1.DeleteOptions{}
		if pvc, ok := existing[name]; ok {
			options.Preconditions = &metav1.Preconditions{UID: &pvc.UID, ResourceVersion: &pvc.ResourceVersion}
		}
		if err := retryStorageDelete(ctx, func() error { return claims.Delete(ctx, name, options) }); err != nil {
			return fmt.Errorf("deleting PVC %s: %w", name, err)
		}
	}
	if err := retryStorageDelete(ctx, func() error { return DeleteNamespace(ctx, namespace) }); err != nil {
		return fmt.Errorf("deleting namespace %s: %w", namespace, err)
	}
	err = wait.PollUntilContextCancel(ctx, time.Second, true, func(ctx context.Context) (bool, error) {
		_, err := GetNamespace(ctx, namespace)
		if err != nil && !apierrors.IsNotFound(err) {
			return false, err
		}
		namespaceGone := apierrors.IsNotFound(err)
		// Check claims independently too: do not depend on namespace deletion alone.
		remaining, err := claims.List(ctx, metav1.ListOptions{})
		if err != nil && !apierrors.IsNotFound(err) {
			return false, err
		}
		claimsGone := remaining == nil || len(remaining.Items) == 0
		if preview {
			volumes, err := getClient().CoreV1().PersistentVolumes().List(ctx, metav1.ListOptions{})
			if err != nil {
				return false, err
			}
			for _, pv := range volumes.Items {
				if ref := pv.Spec.ClaimRef; ref != nil && ref.Namespace == namespace {
					return false, nil
				}
			}
		}
		return namespaceGone && claimsGone, nil
	})
	if err != nil {
		return fmt.Errorf("waiting for namespace %s and storage cleanup (retry is safe): %w", namespace, err)
	}
	return nil
}

func retryStorageDelete(ctx context.Context, remove func() error) error {
	return retry.OnError(wait.Backoff{Steps: 4, Duration: 100 * time.Millisecond, Factor: 2, Jitter: 0.1}, func(err error) bool {
		return apierrors.IsTimeout(err) || apierrors.IsServerTimeout(err) || apierrors.IsTooManyRequests(err) || apierrors.IsServiceUnavailable(err)
	}, func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := remove()
		if apierrors.IsNotFound(err) {
			return nil
		}
		return err
	})
}
