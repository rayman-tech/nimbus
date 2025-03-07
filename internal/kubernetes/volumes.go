package kubernetes

import (
	"nimbus/internal/database"
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

func GetVolumeIdentifiers(namespace string, service *models.Service) (map[string]VolumeInfo, error) {
	volumeMap := make(map[string]VolumeInfo)
	names := make([]string, 0, len(service.Volumes))

	for _, volume := range service.Volumes {
		identifier, err := database.GetQueries().GetVolumeIdentifier(context.TODO(), database.GetVolumeIdentifierParams{
			VolumeName:  volume.Name,
			ProjectName: namespace,
		})
		if err != nil {
			identifier = uuid.New().String()
			if volume.Size == 0 {
				volume.Size = 100
			}
			err = CreatePVC(namespace, identifier, volume.Size)
			if err != nil {
				log.Printf("Error creating PVC: %s\n", err)
				return nil, err
			}
			_, err := database.GetQueries().CreateVolume(context.TODO(), database.CreateVolumeParams{
				VolumeName:  volume.Name,
				ProjectName: namespace,
				Identifier:  identifier,
				Size:        volume.Size,
			})
			if err != nil {
				log.Printf("Error creating volume: %s\n", err)
				return nil, err
			}
		}
		volumeMap[volume.Name] = VolumeInfo{
			PVC:       fmt.Sprintf("pvc-%s", identifier),
			MountPath: volume.MountPath,
		}
		names = append(names, volume.Name)
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

func CreatePVC(namespace string, identifier string, size int32) error {
	client := getClient().CoreV1().PersistentVolumeClaims(namespace)

	storageClass := os.Getenv("NIMBUS_STORAGE_CLASS")
	_, err := client.Create(context.TODO(), &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("pvc-%s", identifier),
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

func DeletePVC(namespace string, name string) error {
	client := getClient().CoreV1().PersistentVolumeClaims(namespace)

	err := client.Delete(context.TODO(), name, metav1.DeleteOptions{})
	return err
}
