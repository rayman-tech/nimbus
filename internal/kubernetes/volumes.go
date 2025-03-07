package kubernetes

import (
	"nimbus/internal/database"
	"nimbus/internal/models"

	"context"
	"fmt"
	"log"
	"os"

	"github.com/google/uuid"
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
