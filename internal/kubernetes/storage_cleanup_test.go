package kubernetes

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/fake"
	ktesting "k8s.io/client-go/testing"
	"nimbus/internal/config"
	nimbusEnv "nimbus/internal/env"
	"nimbus/internal/models"
)

func storageClient(t *testing.T, objects ...runtime.Object) *fake.Clientset {
	t.Helper()
	old := client
	c := fake.NewSimpleClientset(objects...)
	client = c
	t.Cleanup(func() { client = old })
	return c
}

func TestVolumeStorageClasses(t *testing.T) {
	for _, branch := range []string{"", "main", "master", "feature/storage", "maintenance"} {
		t.Run(branch, func(t *testing.T) {
			policy := corev1.PersistentVolumeReclaimDelete
			c := storageClient(t, &storagev1.StorageClass{ObjectMeta: metav1.ObjectMeta{Name: "preview"}, ReclaimPolicy: &policy})
			cfg := &config.Config{NimbusStorageClass: "production", PreviewStorageClass: "preview"}
			id := uuid.New()
			if err := CreatePVC(context.Background(), "app", id, 1024, branch, cfg); err != nil {
				t.Fatal(err)
			}
			pvc, err := c.CoreV1().PersistentVolumeClaims("app").Get(context.Background(), "pvc-"+id.String(), metav1.GetOptions{})
			if err != nil {
				t.Fatal(err)
			}
			want := "preview"
			if branch == "" || branch == "main" || branch == "master" {
				want = "production"
			}
			if pvc.Spec.StorageClassName == nil || *pvc.Spec.StorageClassName != want {
				t.Fatalf("class=%v, want %s", pvc.Spec.StorageClassName, want)
			}
		})
	}
}

func TestPreviewRejectsMissingOrRetainedStorageClass(t *testing.T) {
	for _, name := range []string{"unset", "missing", "retain", "unspecified-policy"} {
		t.Run(name, func(t *testing.T) {
			policy := corev1.PersistentVolumeReclaimRetain
			sc := &storagev1.StorageClass{ObjectMeta: metav1.ObjectMeta{Name: name}, ReclaimPolicy: &policy}
			if name == "unspecified-policy" {
				sc.ReclaimPolicy = nil
			}
			c := storageClient(t, sc)
			cfg := &config.Config{NimbusStorageClass: "production", PreviewStorageClass: name}
			if name == "unset" {
				cfg.PreviewStorageClass = ""
			}
			if name == "missing" {
				cfg.PreviewStorageClass = "not-found"
			}
			if err := CreatePVC(context.Background(), "preview", uuid.New(), 100, "feature", cfg); err == nil {
				t.Fatal("accepted unsafe preview storage")
			}
			pvcs, _ := c.CoreV1().PersistentVolumeClaims("preview").List(context.Background(), metav1.ListOptions{})
			if len(pvcs.Items) != 0 {
				t.Fatal("created a claim despite invalid preview class")
			}
		})
	}
}

func cleanupObjects(policy corev1.PersistentVolumeReclaimPolicy) []runtime.Object {
	return []runtime.Object{
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "preview", UID: "namespace-uid"}},
		&corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Name: "ledger", Namespace: "preview", UID: "claim-uid"}, Spec: corev1.PersistentVolumeClaimSpec{VolumeName: "preview-pv"}},
		&corev1.PersistentVolume{ObjectMeta: metav1.ObjectMeta{Name: "preview-pv"}, Spec: corev1.PersistentVolumeSpec{PersistentVolumeReclaimPolicy: policy, ClaimRef: &corev1.ObjectReference{Namespace: "preview", Name: "ledger", UID: "claim-uid"}}},
		&corev1.PersistentVolume{ObjectMeta: metav1.ObjectMeta{Name: "production-pv"}, Spec: corev1.PersistentVolumeSpec{PersistentVolumeReclaimPolicy: corev1.PersistentVolumeReclaimRetain, ClaimRef: &corev1.ObjectReference{Namespace: "production", Name: "ledger", UID: "other-uid"}}},
	}
}

func TestPreviewCleanupWaitsForProvisionerAndCanRetry(t *testing.T) {
	c := storageClient(t, cleanupObjects(corev1.PersistentVolumeReclaimDelete)...)
	// A fake client has no provisioner. Deleting claims must not count as complete
	// while the PV remains, nor may Nimbus force-delete it to bypass reclamation.
	err := DeleteBranchStorage(context.Background(), "preview", []string{"ledger"}, true, 20*time.Millisecond)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected incomplete reclamation timeout, got %v", err)
	}
	for _, a := range c.Actions() {
		if a.GetVerb() == "delete" && a.GetResource().Resource == "persistentvolumes" {
			t.Fatal("Nimbus force-deleted a PV")
		}
	}
	if _, err := c.CoreV1().PersistentVolumeClaims("preview").Get(context.Background(), "ledger", metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatal("claim was not deleted")
	}
	// Retry still discovers the orphaned PV by claimRef despite the missing PVC.
	err = DeleteBranchStorage(context.Background(), "preview", []string{"ledger"}, true, 20*time.Millisecond)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("lost orphan PV on retry: %v", err)
	}
	if err := c.CoreV1().PersistentVolumes().Delete(context.Background(), "preview-pv", metav1.DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := DeleteBranchStorage(context.Background(), "preview", []string{"ledger"}, true, time.Second); err != nil {
		t.Fatal(err)
	}
	if _, err := c.CoreV1().PersistentVolumes().Get(context.Background(), "production-pv", metav1.GetOptions{}); err != nil {
		t.Fatal("unrelated production PV changed", err)
	}
}

func TestRetainedPreviewFailsBeforeDeletingClaims(t *testing.T) {
	c := storageClient(t, cleanupObjects(corev1.PersistentVolumeReclaimRetain)...)
	err := DeleteBranchStorage(context.Background(), "preview", []string{"ledger"}, true, time.Second)
	if err == nil || !strings.Contains(err.Error(), "explicitly migrate") {
		t.Fatalf("got %v", err)
	}
	for _, a := range c.Actions() {
		if a.GetVerb() == "delete" {
			t.Fatalf("mutated retained preview: %v", a)
		}
	}
}

func TestProductionCleanupPreservesRetainedPV(t *testing.T) {
	c := storageClient(t, cleanupObjects(corev1.PersistentVolumeReclaimRetain)...)
	if err := DeleteBranchStorage(context.Background(), "preview", []string{"ledger"}, false, time.Second); err != nil {
		t.Fatal(err)
	}
	if _, err := c.CoreV1().PersistentVolumes().Get(context.Background(), "preview-pv", metav1.GetOptions{}); err != nil {
		t.Fatal("production PV changed", err)
	}
}

func TestCleanupErrorsAndNamespaceFinalizers(t *testing.T) {
	for _, resource := range []string{"persistentvolumeclaims", "namespaces"} {
		t.Run(resource, func(t *testing.T) {
			c := storageClient(t, cleanupObjects(corev1.PersistentVolumeReclaimRetain)...)
			c.PrependReactor("delete", resource, func(a ktesting.Action) (bool, runtime.Object, error) {
				return true, nil, apierrors.NewForbidden(schema.GroupResource{Resource: resource}, "blocked", errors.New("denied"))
			})
			if err := DeleteBranchStorage(context.Background(), "preview", []string{"ledger"}, false, time.Second); !apierrors.IsForbidden(err) {
				t.Fatalf("deletion error was lost: %v", err)
			}
		})
	}
	t.Run("namespace still terminating", func(t *testing.T) {
		c := storageClient(t, cleanupObjects(corev1.PersistentVolumeReclaimRetain)...)
		c.PrependReactor("delete", "namespaces", func(a ktesting.Action) (bool, runtime.Object, error) { return true, nil, nil })
		if err := DeleteBranchStorage(context.Background(), "preview", nil, false, 20*time.Millisecond); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("returned before namespace deleted: %v", err)
		}
	})
	t.Run("transient API failure retries", func(t *testing.T) {
		c := storageClient(t, cleanupObjects(corev1.PersistentVolumeReclaimRetain)...)
		calls := 0
		c.PrependReactor("delete", "persistentvolumeclaims", func(a ktesting.Action) (bool, runtime.Object, error) {
			calls++
			if calls == 1 {
				return true, nil, apierrors.NewServiceUnavailable("retry")
			}
			return false, nil, nil
		})
		if err := DeleteBranchStorage(context.Background(), "preview", nil, false, time.Second); err != nil {
			t.Fatal(err)
		}
		if calls != 2 {
			t.Fatalf("attempts=%d", calls)
		}
	})
	t.Run("claim UID mismatch", func(t *testing.T) {
		objects := cleanupObjects(corev1.PersistentVolumeReclaimDelete)
		objects[1].(*corev1.PersistentVolumeClaim).UID = types.UID("replacement")
		c := storageClient(t, objects...)
		if err := DeleteBranchStorage(context.Background(), "preview", nil, true, time.Second); err == nil {
			t.Fatal("ignored claim UID mismatch")
		}
		for _, a := range c.Actions() {
			if a.GetVerb() == "delete" {
				t.Fatal("deleted mismatched claim")
			}
		}
	})
}

func TestOrdinaryPreviewRedeployReusesExistingClaim(t *testing.T) {
	id := uuid.New()
	oldClass := "retained-legacy-class"
	c := storageClient(t, &corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Name: "pvc-" + id.String(), Namespace: "preview", UID: "unchanged"}, Spec: corev1.PersistentVolumeClaimSpec{StorageClassName: &oldClass}})
	got, err := GetVolumeIdentifiers(context.Background(), &models.Service{Volumes: []models.Volume{{Name: "ledger", MountPath: "/data", Size: 100}}}, &models.DeployRequest{Namespace: "preview", BranchName: "feature/test"}, &nimbusEnv.Env{Database: stubQuerier{identifier: id}, Config: &config.Config{PreviewStorageClass: "new-delete-class"}})
	if err != nil {
		t.Fatal(err)
	}
	if got["ledger"].PVC != "pvc-"+id.String() {
		t.Fatal("changed existing volume identity")
	}
	for _, a := range c.Actions() {
		if a.GetVerb() != "get" {
			t.Fatalf("redeploy mutated existing storage: %v", a)
		}
	}
}
