package kubernetes

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"nimbus/internal/config"
	"nimbus/internal/database"
	"nimbus/internal/env"
	"nimbus/internal/models"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type VolumeInfo struct {
	PVC       string
	MountPath string
	Size      int32
}

func GetVolumeIdentifiers(
	ctx context.Context, service *models.Service,
	deploymentRequest *models.DeployRequest, env *env.Env,
) (map[string]VolumeInfo, error) {
	volumeMap := make(map[string]VolumeInfo)

	for _, volume := range service.Volumes {
		if volume.Size == 0 {
			volume.Size = 100 // default to 100Mi
		}

		identifier, err := env.Database.GetVolumeIdentifier(ctx, database.GetVolumeIdentifierParams{
			VolumeName:    volume.Name,
			ProjectID:     deploymentRequest.ProjectID,
			ProjectBranch: deploymentRequest.BranchName,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			slog.DebugContext(
				ctx, "volume identifier does not exist - creating one",
				"volume-name", volume.Name,
				"branch-name", deploymentRequest.BranchName)
			identifier = uuid.New()
			err = CreatePVC(ctx, deploymentRequest.Namespace, identifier, volume.Size, env.Config)
			if err != nil {
				return nil, fmt.Errorf("creating pvc: %w", err)
			}
			_, err := env.Database.CreateVolume(ctx, database.CreateVolumeParams{
				Identifier:    identifier,
				VolumeName:    volume.Name,
				ProjectID:     deploymentRequest.ProjectID,
				ProjectBranch: deploymentRequest.BranchName,
				Size:          volume.Size,
			})
			if err != nil {
				return nil, fmt.Errorf("creating volume in database: %w", err)
			}
		} else if err != nil {
			return nil, fmt.Errorf("getting volume identifier: %w", err)
		} else if !CheckPVC(ctx, deploymentRequest.Namespace, fmt.Sprintf("pvc-%s", identifier)) {
			// ensure PVC in database actually exists (sanity check)
			err = CreatePVC(ctx, deploymentRequest.Namespace, identifier, volume.Size, env.Config)
			if err != nil {
				slog.ErrorContext(ctx, "failed to create PVC", "error", err)
				return nil, err
			}
		}

		volumeMap[volume.Name] = VolumeInfo{
			PVC:       fmt.Sprintf("pvc-%s", identifier),
			MountPath: volume.MountPath,
		}
	}

	return volumeMap, nil
}

func CheckPVC(ctx context.Context, namespace string, name string) bool {
	client := getClient().CoreV1().PersistentVolumeClaims(namespace)

	_, err := client.Get(ctx, name, metav1.GetOptions{})
	return err == nil
}

func CreatePVC(ctx context.Context, namespace string, identifier uuid.UUID, size int32, cfg *config.Config) error {
	client := getClient().CoreV1().PersistentVolumeClaims(namespace)

	_, err := client.Create(ctx, &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("pvc-%s", identifier.String()),
			Namespace: namespace,
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{
				corev1.ReadWriteOnce,
			},
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceStorage: resource.MustParse(fmt.Sprintf("%dMi", size)),
				},
			},
			StorageClassName: &cfg.NimbusStorageClass,
		},
	}, metav1.CreateOptions{})

	return err
}

func DeletePVC(ctx context.Context, namespace string, name string) error {
	client := getClient().CoreV1().PersistentVolumeClaims(namespace)

	err := client.Delete(ctx, name, metav1.DeleteOptions{})
	if err != nil && !k8serrors.IsNotFound(err) {
		return err
	}
	return nil
}
