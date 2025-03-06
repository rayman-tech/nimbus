package handlers

import (
	"nimbus/internal/database"
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

	apiKey := r.Header.Get("X-API-Key")
	if apiKey == "" {
		log.Println("API key missing")
		http.Error(w, "API key missing", http.StatusUnauthorized)
		return
	}

	project, err := database.GetQueries().GetProjectByApiKey(r.Context(), apiKey)
	if err != nil {
		log.Printf("Error getting project: %s\n", err)
		http.Error(w, "Error getting project", http.StatusUnauthorized)
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

	if config.App != project.Name {
		log.Printf("App name does not match project name: %s != %s\n", config.App, project.Name)
		http.Error(w, "App name does not match project name", http.StatusBadRequest)
		return
	}

	existingServices, err := database.GetQueries().GetServicesByProject(r.Context(), project.Name)
	if err != nil {
		log.Printf("Error retrieving project services: %s\n", err)
		http.Error(w, "Error getting project services", http.StatusInternalServerError)
		return
	}
	var serviceMap = make(map[string]*database.Service)
	for _, service := range existingServices {
		serviceMap[service.Name] = &service
	}

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
		deploymentSpec, err := services.GenerateDeploymentSpec(config.App, &service)
		if err != nil {
			log.Printf("Error generating deployment spec: %s\n", err)
			http.Error(w, "Error generating deployment spec", http.StatusInternalServerError)
			return
		}
		newDeployment, err := services.CreateDeployment(config.App, deploymentSpec)
		if err != nil {
			log.Printf("Error creating deployment: %s\n", err)
			http.Error(w, "Error creating deployment", http.StatusInternalServerError)
			return
		}
		log.Printf("Created deployment: %s\n", newDeployment.Name)

		log.Printf("Creating service for deployment: %s\n", newDeployment.Name)
		svc := serviceMap[service.Name]
		serviceSpec, err := services.GenerateServiceSpec(config.App, &service, svc)
		if err != nil {
			log.Printf("Error generating service spec: %s\n", err)
			http.Error(w, "Error generating service spec", http.StatusInternalServerError)
			return
		}
		newService, err := services.CreateService(config.App, serviceSpec)
		if err != nil {
			log.Printf("Error creating service: %s\n", err)
			http.Error(w, "Error creating service", http.StatusInternalServerError)
			return
		}
		log.Printf("Created service: %s\n", newService.Name)

		if service.Template == "http" {
			log.Printf("Creating ingress for service: %s\n", newService.Name)
			ingressSpec, err := services.GenerateIngressSpec(config.App, &service, svc)
			if err != nil {
				log.Printf("Error generating ingress spec: %s\n", err)
				http.Error(w, "Error generating ingress spec", http.StatusInternalServerError)
				return
			}
			newIngress, err := services.CreateIngress(config.App, ingressSpec)
			if err != nil {
				log.Printf("Error creating ingress: %s\n", err)
				http.Error(w, "Error creating ingress", http.StatusInternalServerError)
				return
			}
			log.Printf("Created ingress: %s\n", newIngress.Name)
		}
	}

	w.WriteHeader(http.StatusOK)
}
