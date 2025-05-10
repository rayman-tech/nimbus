package kubernetes

import (
	"nimbus/internal/database"
	nimbusEnv "nimbus/internal/env"
	"nimbus/internal/models"

	"context"
	"fmt"
	"log"
	"os"

	"github.com/google/uuid"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type VolumeInfo struct {
	PVC       string
	MountPath string
	Size      int32
}

func GetVolumeIdentifiers(namespace string, service *models.Service, env *nimbusEnv.Env) (map[string]VolumeInfo, error) {
	volumeMap := make(map[string]VolumeInfo)

	for _, volume := range service.Volumes {
		if volume.Size == 0 {
			volume.Size = 100 // default to 100Mi
		}

		identifier, err := env.GetVolumeIdentifier(context.TODO(), database.GetVolumeIdentifierParams{
			VolumeName:  volume.Name,
			ProjectName: namespace,
		})
		if err != nil {
			identifier = uuid.New()
			err = CreatePVC(namespace, identifier, volume.Size, env)
			if err != nil {
				log.Printf("Error creating PVC: %s\n", err)
				return nil, err
			}
			_, err := env.CreateVolume(context.TODO(), database.CreateVolumeParams{
				VolumeName:  volume.Name,
				ProjectName: namespace,
				Identifier:  identifier,
				Size:        volume.Size,
			})
			if err != nil {
				log.Printf("Error creating volume: %s\n", err)
				return nil, err
			}
		} else if !CheckPVC(namespace, fmt.Sprintf("pvc-%s", identifier), env) {
			// ensure PVC in database actually exists (sanity check)
			err = CreatePVC(namespace, identifier, volume.Size, env)
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

	// TODO: migrate to project-wide context to prevent deleting other services' volumes
	// unusedIdentifiers, err := database.GetQueries().GetUnusedVolumeIdentifiers(context.TODO(), database.GetUnusedVolumeIdentifiersParams{
	// 	ProjectName: namespace,
	// 	Column2:     names,
	// })
	// if err != nil {
	// 	log.Printf("Error getting unused volume identifiers: %s\n", err)
	// 	return volumeMap, nil
	// }
	// for _, identifier := range unusedIdentifiers {
	// 	DeletePVC(namespace, fmt.Sprintf("pvc-%s", identifier))
	// }

	// err = database.GetQueries().DeleteUnusedVolumes(context.TODO(), database.DeleteUnusedVolumesParams{
	// 	ProjectName: namespace,
	// 	Column2:     names,
	// })
	// if err != nil {
	// 	log.Printf("Error deleting unused volumes: %s\n", err)
	// }

	return volumeMap, nil
}

func CheckPVC(namespace string, name string, env *nimbusEnv.Env) bool {
	client := getClient(env).CoreV1().PersistentVolumeClaims(namespace)

	_, err := client.Get(context.TODO(), name, metav1.GetOptions{})
	return err == nil
}

func CreatePVC(namespace string, identifier uuid.UUID, size int32, env *nimbusEnv.Env) error {
	client := getClient(env).CoreV1().PersistentVolumeClaims(namespace)

	storageClass := os.Getenv("NIMBUS_STORAGE_CLASS")
	_, err := client.Create(context.TODO(), &corev1.PersistentVolumeClaim{
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
			StorageClassName: &storageClass,
		},
	}, metav1.CreateOptions{})

	return err
}

func DeletePVC(namespace string, name string, env *nimbusEnv.Env) error {
	client := getClient(env).CoreV1().PersistentVolumeClaims(namespace)

	err := client.Delete(context.TODO(), name, metav1.DeleteOptions{})
	return err
}
