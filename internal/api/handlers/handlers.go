package handlers

import (
	"nimbus/internal/database"
	"nimbus/internal/kubernetes"
	"nimbus/internal/models"

	"io"
	"log"
	"net/http"
	"os"

	"github.com/jackc/pgx/v5/pgtype"
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

	if os.Getenv("NIMBUS_PVC") == "" {
		log.Println("NIMBUS_PVC environment variable not set")
		http.Error(w, "Server is missing environment variables", http.StatusInternalServerError)
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

	namespace, err := kubernetes.GetNamespace(config.App)
	if err != nil || namespace == nil {
		err = kubernetes.CreateNamespace(config.App)
		if err != nil {
			log.Printf("Error creating namespace: %s\n", config.App)
			http.Error(w, "Error creating namespace", http.StatusInternalServerError)
			return
		}
		log.Printf("Created namespace: %s\n", config.App)
	}

	serviceNames := make(map[string]bool)
	for _, service := range config.Services {
		if serviceNames[service.Name] {
			log.Printf("Service names must be unique: %s\n", service.Name)
			http.Error(w, "Service names must be unique, duplicate of "+service.Name, http.StatusBadRequest)
			return
		}
		serviceNames[service.Name] = true
	}

	for _, service := range existingServices {
		if _, ok := serviceNames[service.Name]; !ok {
			log.Printf("Deleting deployment for service: %s\n", service.Name)
			err = kubernetes.DeleteDeployment(config.App, service.Name)
			if err != nil {
				log.Printf("Error deleting deployment: %s\n", err)
				http.Error(w, "Error deleting deployment", http.StatusInternalServerError)
				return
			}
			log.Printf("Deleted deployment: %s\n", service.Name)

			log.Printf("Deleting service for service: %s\n", service.Name)
			err = kubernetes.DeleteService(config.App, service.Name)
			if err != nil {
				log.Printf("Error deleting service: %s\n", err)
				http.Error(w, "Error deleting service", http.StatusInternalServerError)
				return
			}
			log.Printf("Deleted service: %s\n", service.Name)

			if service.Ingress.Valid {
				log.Printf("Deleting ingress for service: %s\n", service.Name)
				err = kubernetes.DeleteIngress(config.App, service.Ingress.String)
				if err != nil {
					log.Printf("Error deleting ingress: %s\n", err)
					http.Error(w, "Error deleting ingress", http.StatusInternalServerError)
					return
				}
				log.Printf("Deleted ingress: %s\n", service.Name)
			}

			log.Printf("Deleting service in database: %s\n", service.Name)
			err = database.GetQueries().DeleteService(r.Context(), database.DeleteServiceParams{
				Name:        service.Name,
				ProjectName: project.Name,
			})
			if err != nil {
				log.Printf("Error deleting service in database: %s\n", err)
				http.Error(w, "Error deleting service", http.StatusInternalServerError)
				return
			}
		}
	}

	for _, service := range config.Services {
		// --- Deployment ---
		log.Printf("Creating deployment for service: %s\n", service.Name)
		deploymentSpec, err := kubernetes.GenerateDeploymentSpec(config.App, &service)
		if err != nil {
			log.Printf("Error generating deployment spec: %s\n", err)
			http.Error(w, "Error generating deployment spec", http.StatusInternalServerError)
			return
		}
		newDeployment, err := kubernetes.CreateDeployment(config.App, deploymentSpec)
		if err != nil {
			log.Printf("Error creating deployment: %s\n", err)
			http.Error(w, "Error creating deployment", http.StatusInternalServerError)
			return
		}
		log.Printf("Created deployment: %s\n", newDeployment.Name)

		// --- Service ---
		log.Printf("Creating service for deployment: %s\n", newDeployment.Name)
		svc, svcExists := serviceMap[service.Name]
		serviceSpec, err := kubernetes.GenerateServiceSpec(config.App, &service, svc)
		if err != nil {
			log.Printf("Error generating service spec: %s\n", err)
			http.Error(w, "Error generating service spec", http.StatusInternalServerError)
			return
		}
		newService, err := kubernetes.CreateService(config.App, serviceSpec)
		if err != nil {
			log.Printf("Error creating service: %s\n", err)
			http.Error(w, "Error creating service", http.StatusInternalServerError)
			return
		}
		log.Printf("Created service: %s\n", newService.Name)
		if svcExists && service.Template != "http" {
			// TODO: only run this if the service node ports changed
			log.Printf("Updating service in database: %s\n", newService.Name)
			var nodePorts []int32
			for _, port := range newService.Spec.Ports {
				nodePorts = append(nodePorts, port.NodePort)
			}
			err = database.GetQueries().SetServiceNodePorts(r.Context(), database.SetServiceNodePortsParams{
				Name:        newService.Name,
				ProjectName: project.Name,
				NodePorts:   nodePorts,
			})
			if err != nil {
				log.Printf("Error updating service in database: %s\n", err)
				http.Error(w, "Error updating service", http.StatusInternalServerError)
				return
			}
		} else if !svcExists {
			log.Printf("Creating service in database: %s\n", newService.Name)
			var nodePorts []int32
			if service.Template != "http" {
				for _, port := range newService.Spec.Ports {
					nodePorts = append(nodePorts, port.NodePort)
				}
			}
			_, err = database.GetQueries().CreateService(r.Context(), database.CreateServiceParams{
				Name:        newService.Name,
				ProjectName: project.Name,
				NodePorts:   nodePorts,
			})
			if err != nil {
				log.Printf("Error creating service in database: %s\n", err)
				http.Error(w, "Error creating service", http.StatusInternalServerError)
				return
			}
			svcExists = true
		}

		// --- Ingress ---
		if service.Template == "http" {
			log.Printf("Creating ingress for service: %s\n", newService.Name)
			ingressSpec, err := kubernetes.GenerateIngressSpec(config.App, &service, svc)
			if err != nil {
				log.Printf("Error generating ingress spec: %s\n", err)
				http.Error(w, "Error generating ingress spec", http.StatusInternalServerError)
				return
			}
			newIngress, err := kubernetes.CreateIngress(config.App, ingressSpec)
			if err != nil {
				log.Printf("Error creating ingress: %s\n", err)
				http.Error(w, "Error creating ingress", http.StatusInternalServerError)
				return
			}
			log.Printf("Created ingress: %s\n", newIngress.Name)

			if svcExists {
				log.Printf("Updating ingress in database: %s\n", newIngress.Spec.Rules[0].Host)
				log.Printf("Params: %s %s\n", service.Name, project.Name)
				err = database.GetQueries().SetServiceIngress(r.Context(), database.SetServiceIngressParams{
					Name:        service.Name,
					ProjectName: project.Name,
					Ingress:     pgtype.Text{String: newIngress.Spec.Rules[0].Host, Valid: true},
				})
				if err != nil {
					log.Printf("Error updating ingress in database: %s\n", err)
					http.Error(w, "Error updating ingress", http.StatusInternalServerError)
					return
				}
			} else {
				log.Printf("Creating ingress in database: %s\n", newIngress.Spec.Rules[0].Host)
				_, err = database.GetQueries().CreateService(r.Context(), database.CreateServiceParams{
					Name:        service.Name,
					ProjectName: project.Name,
					Ingress:     pgtype.Text{String: newIngress.Spec.Rules[0].Host, Valid: true},
				})
				if err != nil {
					log.Printf("Error creating ingress in database: %s\n", err)
					http.Error(w, "Error creating ingress", http.StatusInternalServerError)
					return
				}
			}
		}
	}

	w.WriteHeader(http.StatusOK)
}
