package kubernetes

import (
	"nimbus/internal/database"
	"nimbus/internal/models"

	"context"
	"log"

	"github.com/google/uuid"
)

type VolumeInfo struct {
	Identifier string
	MountPath  string
}

func GetVolumeIdentifiers(namespace string, serviceName string, volumes []models.Volume) (map[string]VolumeInfo, error) {
	volumeMap := make(map[string]VolumeInfo)
	names := make([]string, 0, len(volumes))

	for _, volume := range volumes {
		identifier, err := database.GetQueries().GetVolumeIdentifier(context.TODO(), database.GetVolumeIdentifierParams{
			VolumeName:  volume.Name,
			ProjectName: namespace,
			ServiceName: serviceName,
		})
		if err != nil {
			identifier = uuid.New().String()
			_, err := database.GetQueries().CreateVolume(context.TODO(), database.CreateVolumeParams{
				VolumeName:  volume.Name,
				ProjectName: namespace,
				ServiceName: serviceName,
				Identifier:  identifier,
			})
			if err != nil {
				log.Printf("Error creating volume: %s\n", err)
				return nil, err
			}
		}
		volumeMap[volume.Name] = VolumeInfo{
			Identifier: identifier,
			MountPath:  volume.MountPath,
		}
		names = append(names, volume.Name)
	}

	err := database.GetQueries().DeleteUnusedVolumes(context.TODO(), database.DeleteUnusedVolumesParams{
		ProjectName: namespace,
		ServiceName: serviceName,
		VolumeNames: names,
	})
	if err != nil {
		log.Printf("Error deleting unused volumes: %s\n", err)
		return nil, err
	}

	return volumeMap, nil
}
