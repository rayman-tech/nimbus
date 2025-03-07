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
	Identifier string
	MountPath  string
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
			_, err := database.GetQueries().CreateVolume(context.TODO(), database.CreateVolumeParams{
				VolumeName:  volume.Name,
				ProjectName: namespace,
				Identifier:  identifier,
			})
			if err != nil {
				log.Printf("Error creating volume: %s\n", err)
				return nil, err
			}
			err = os.MkdirAll(fmt.Sprintf("/volumes/%s/%s", namespace, identifier), 0755)
		}
		volumeMap[volume.Name] = VolumeInfo{
			Identifier: identifier,
			MountPath:  volume.MountPath,
		}
		names = append(names, volume.Name)
	}

	unusedIdentifiers, err := database.GetQueries().GetUnusedVolumeIdentifiers(context.TODO(), database.GetUnusedVolumeIdentifiersParams{
		ProjectName: namespace,
		Column2:     names,
	})
	if err != nil {
		log.Printf("Error getting unused volume identifiers: %s\n", err)
		return volumeMap, nil
	}
	for _, identifier := range unusedIdentifiers {
		err := os.RemoveAll(fmt.Sprintf("/volumes/%s/%s", namespace, identifier))
		if err != nil {
			log.Printf("Error removing volume: %s\n", err)
		}
	}

	err = database.GetQueries().DeleteUnusedVolumes(context.TODO(), database.DeleteUnusedVolumesParams{
		ProjectName: namespace,
		Column2:     names,
	})
	if err != nil {
		log.Printf("Error deleting unused volumes: %s\n", err)
	}

	return volumeMap, nil
}

func InitNamespacePVC(namespace string) error {
	client := getClient().CoreV1().PersistentVolumeClaims(namespace)

	_, err := client.Get(context.TODO(), "nimbus-data-pvc", metav1.GetOptions{})
	if err == nil {
		return nil
	}

	_, err = client.Create(context.TODO(), &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "nimbus-data-pvc",
			Namespace: namespace,
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{
				corev1.ReadWriteMany,
			},
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceStorage: resource.MustParse("100Gi"),
				},
			},
			VolumeName: os.Getenv("NIMBUS_PV"),
		},
	}, metav1.CreateOptions{})

	return err
}
