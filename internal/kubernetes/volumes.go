package kubernetes

import (
	"context"
	"errors"
	"fmt"
	"log"
	"log/slog"

	"nimbus/internal/database"
	"nimbus/internal/env"
	"nimbus/internal/models"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
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
			env.Logger.DebugContext(
				ctx, "volume identifier does not exist - creating one",
				slog.String("volume-name", volume.Name),
				slog.String("branch-name", deploymentRequest.BranchName))
			identifier = uuid.New()
			err = CreatePVC(ctx, deploymentRequest.Namespace, identifier, volume.Size, env)
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
		} else if !CheckPVC(ctx, deploymentRequest.Namespace, fmt.Sprintf("pvc-%s", identifier), env) {
			// ensure PVC in database actually exists (sanity check)
			err = CreatePVC(ctx, deploymentRequest.Namespace, identifier, volume.Size, env)
			if err != nil {
				log.Printf("Error creating PVC: %s\n", err)
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

func CheckPVC(ctx context.Context, namespace string, name string, env *env.Env) bool {
	client := getClient(env).CoreV1().PersistentVolumeClaims(namespace)

	_, err := client.Get(ctx, name, metav1.GetOptions{})
	return err == nil
}

func CreatePVC(ctx context.Context, namespace string, identifier uuid.UUID, size int32, env *env.Env) error {
	client := getClient(env).CoreV1().PersistentVolumeClaims(namespace)

	_, err := client.Create(ctx, &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("pvc-%s", identifier.String()),
			Namespace: namespace,
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{
				corev1.ReadWriteMany,
			},
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceStorage: resource.MustParse(fmt.Sprintf("%dMi", size)),
				},
			},
			StorageClassName: &env.Config.NimbusStorageClass,
		},
	}, metav1.CreateOptions{})

	return err
}

func DeletePVC(ctx context.Context, namespace string, name string, env *env.Env) error {
	client := getClient(env).CoreV1().PersistentVolumeClaims(namespace)

	err := client.Delete(ctx, name, metav1.DeleteOptions{})
	return err
}
