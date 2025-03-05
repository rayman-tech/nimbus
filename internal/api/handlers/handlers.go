package handlers

import (
	"nimbus/internal/models"
	"nimbus/internal/services"

	"io"
	"log"
	"net/http"

	"gopkg.in/yaml.v3"
)

func Deploy(w http.ResponseWriter, r *http.Request) {
	log.Println("POST /deploy")

	err := r.ParseMultipartForm(512 << 20)
	if err != nil {
		log.Printf("Failed to parse multipart form: %s\n", err)
		http.Error(w, "Failed to parse multipart form", http.StatusBadRequest)
		return
	}

	file, handler, err := r.FormFile("file")
	if err != nil {
		log.Printf("Error retrieving the file: %s\n", err)
		http.Error(w, "Error retrieving the file", http.StatusBadRequest)
		return
	}

	defer file.Close()

	log.Printf("Received file: %s\n", handler.Filename)

	content, err := io.ReadAll(file)
	if err != nil {
		http.Error(w, "Error reading file", http.StatusInternalServerError)
		return
	}

	var config models.Config
	err = yaml.Unmarshal(content, &config)
	if err != nil {
		log.Printf("Error parsing YAML: %s\n", err)
		http.Error(w, "Error parsing YAML", http.StatusBadRequest)
		return
	}
	log.Printf("Parsed YAML: %+v\n", config)

	namespace, err := services.GetNamespace(config.App)
	if err != nil || namespace == nil {
		err = services.CreateNamespace(config.App)
		if err != nil {
			log.Printf("Error creating namespace: %s\n", config.App)
			http.Error(w, "Error creating namespace", http.StatusInternalServerError)
			return
		}
		log.Printf("Created namespace: %s\n", config.App)
	}

	for _, service := range config.Services {
		log.Printf("Creating deployment for service: %s\n", service.Name)
		spec, err := services.GenerateDeploymentSpec(config.App, &service)
		if err != nil {
			log.Printf("Error generating deployment spec: %s\n", err)
			http.Error(w, "Error generating deployment spec", http.StatusInternalServerError)
			return
		}
		deployment, err := services.StartDeployment(config.App, spec)
		if err != nil {
			log.Printf("Error creating deployment: %s\n", err)
			http.Error(w, "Error creating deployment", http.StatusInternalServerError)
			return
		}
		log.Printf("Created deployment: %s\n", deployment.Name)
	}

	w.WriteHeader(http.StatusOK)
}
