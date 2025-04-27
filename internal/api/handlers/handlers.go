package handlers

import (
	"nimbus/internal/database"
	nimbusEnv "nimbus/internal/env"
	"nimbus/internal/kubernetes"
	"nimbus/internal/models"

	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"

	"github.com/jackc/pgx/v5/pgtype"
	"gopkg.in/yaml.v3"
)

const formFile = "file"
const xApiKey = "X-API-Key"

const envKey = "env"

func Deploy(w http.ResponseWriter, r *http.Request) {
	env, ok := r.Context().Value(envKey).(*nimbusEnv.Env)
	if !ok {
		env = nimbusEnv.Null()
	}

	db := env.Database
	log := env.Logger
	ctx := r.Context()

	log.DebugContext(ctx, "Parsing form")
	err := r.ParseMultipartForm(512 << 20)
	if err != nil {
		log.LogAttrs(
			ctx,
			slog.LevelError,
			"Failed to parse multipart form",
			slog.Any("error", err),
		)
		http.Error(w, "Failed to parse multipart form", http.StatusBadRequest)
		return
	}

	log.DebugContext(ctx, "Retrieving file from form")
	file, handler, err := r.FormFile(formFile)
	if err != nil {
		log.LogAttrs(
			ctx,
			slog.LevelError,
			"Error retrieving file",
			slog.Any("error", err),
		)
		http.Error(w, "Error retrieving the file", http.StatusBadRequest)
		return
	}

	defer file.Close()
	log = log.With(slog.String("filename", handler.Filename))
	log.DebugContext(ctx, "File received")

	log.DebugContext(ctx, "Reading file content")
	content, err := io.ReadAll(file)
	if err != nil {
		log.LogAttrs(
			ctx,
			slog.LevelError,
			"Error reading file",
			slog.Any("error", err),
		)
		http.Error(w, "Error reading file", http.StatusInternalServerError)
		return
	}

	log.DebugContext(ctx, "Reading API key")
	apiKey := r.Header.Get(xApiKey)
	if apiKey == "" {
		log.ErrorContext(ctx, "API key missing")
		http.Error(w, "API key missing", http.StatusUnauthorized)
		return
	}

	if env.Getenv("NIMBUS_STORAGE_CLASS") == "" {
		log.ErrorContext(ctx, "NIMBUS_STORAGE_CLASS environment variable not set")
		http.Error(w, "Server is missing environment variables", http.StatusInternalServerError)
		return
	}

	log.DebugContext(ctx, "Retrieving project by API key")
	project, err := db.GetProjectByApiKey(r.Context(), apiKey)
	if err != nil {
		log.LogAttrs(
			ctx,
			slog.LevelError,
			"Error getting project by API key",
			slog.Any("error", err),
		)
		http.Error(w, "Error getting project", http.StatusUnauthorized)
		return
	}

	log.DebugContext(ctx, "Unmarshaling YAML file")
	var config models.Config
	err = yaml.Unmarshal(content, &config)
	if err != nil {
		log.LogAttrs(
			ctx,
			slog.LevelError,
			"Error parsing YAML",
			slog.Any("error", err),
		)
		http.Error(w, "Error parsing YAML", http.StatusBadRequest)
		return
	}

	log.DebugContext(ctx, "Validating project name")
	if config.App != project.Name {
		log.LogAttrs(
			ctx,
			slog.LevelError,
			"App name does not match project name",
			slog.String("app", config.App),
			slog.String("project", project.Name),
		)
		http.Error(w, "App name does not match project name", http.StatusBadRequest)
		return
	}
	log = log.With(slog.String("app", config.App))

	log.DebugContext(ctx, "Retrieving project services")
	existingServices, err := db.GetServicesByProject(r.Context(), project.Name)
	if err != nil {
		log.LogAttrs(
			ctx,
			slog.LevelError,
			"Error retrieving project services",
			slog.Any("error", err),
		)
		http.Error(w, "Error getting project services", http.StatusInternalServerError)
		return
	}

	log.DebugContext(ctx, "Creating service map for existing services")
	var serviceMap = make(map[string]*database.Service)
	for _, service := range existingServices {
		serviceMap[service.Name] = &service
	}

	log.DebugContext(ctx, "Retrieving namespace")
	namespace, err := kubernetes.GetNamespace(config.App, env)
	if err != nil || namespace == nil {
		log.LogAttrs(
			ctx,
			slog.LevelError,
			"Error retrieving namespace. Attempting to create it",
			slog.Any("error", err),
		)
		err = kubernetes.CreateNamespace(config.App, env)
		if err != nil {
			log.LogAttrs(
				ctx,
				slog.LevelError,
				"Error creating namespace",
				slog.Any("error", err),
			)
			http.Error(w, "Error creating namespace", http.StatusInternalServerError)
			return
		}
		log.LogAttrs(
			ctx,
			slog.LevelInfo,
			"Namespace created",
			slog.String("namespace", config.App),
		)
	}
	log = log.With(slog.String("namespace", config.App))

	log.DebugContext(ctx, "Processing services in config file")
	serviceNames := make(map[string]bool)
	for _, service := range config.Services {
		log.LogAttrs(ctx, slog.LevelDebug, "Processing service", slog.String("service", service.Name))
		if serviceNames[service.Name] {
			log.LogAttrs(ctx, slog.LevelError, "Service names must be unique", slog.String("service", service.Name))
			http.Error(w, "Service names must be unique, duplicate of "+service.Name, http.StatusBadRequest)
			return
		}
		serviceNames[service.Name] = true
	}

	log.DebugContext(ctx, "Deleting services not in config file")
	for _, service := range existingServices {
		log.LogAttrs(ctx, slog.LevelDebug, "Processing service", slog.String("service", service.Name))

		if _, ok := serviceNames[service.Name]; !ok {
			log.LogAttrs(ctx, slog.LevelDebug, "Deleting deployment", slog.String("service", service.Name))
			err = kubernetes.DeleteDeployment(config.App, service.Name, env)
			if err != nil {
				log.LogAttrs(
					ctx, slog.LevelError, "Error deleting deployment",
					slog.String("service", service.Name), slog.Any("error", err),
				)
				http.Error(w, "Error deleting deployment", http.StatusInternalServerError)
				return
			}

			log.LogAttrs(ctx, slog.LevelDebug, "Deleting service", slog.String("service", service.Name))
			err = kubernetes.DeleteService(config.App, service.Name, env)
			if err != nil {
				log.LogAttrs(
					ctx, slog.LevelError, "Error deleting service",
					slog.String("service", service.Name), slog.Any("error", err),
				)
				http.Error(w, "Error deleting service", http.StatusInternalServerError)
				return
			}

			if service.Ingress.Valid {
				log.LogAttrs(ctx, slog.LevelDebug, "Deleting ingress", slog.String("service", service.Name))
				err = kubernetes.DeleteIngress(config.App, service.Ingress.String, env)
				if err != nil {
					log.LogAttrs(
						ctx, slog.LevelError, "Error deleting ingress",
						slog.String("service", service.Name), slog.Any("error", err),
					)
					http.Error(w, "Error deleting ingress", http.StatusInternalServerError)
					return
				}
			}

			log.LogAttrs(ctx, slog.LevelDebug, "Deleting service in database", slog.String("service", service.Name))
			err = db.DeleteService(r.Context(), database.DeleteServiceParams{
				Name:        service.Name,
				ProjectName: project.Name,
			})
			if err != nil {
				log.LogAttrs(
					ctx, slog.LevelError, "Error deleting service in databse",
					slog.String("service", service.Name), slog.Any("error", err),
				)
				http.Error(w, "Error deleting service", http.StatusInternalServerError)
				return
			}
		}
	}

	log.DebugContext(ctx, "Creating services and deployments")
	serviceUrls := make(map[string][]string)
	for _, service := range config.Services {
		tempCtx := context.WithValue(ctx, "service", service.Name)

		// --- Deployment ---
		log.DebugContext(tempCtx, "Creating deployment")
		log.DebugContext(tempCtx, "Generating deployment spec")
		deploymentSpec, err := kubernetes.GenerateDeploymentSpec(config.App, &service, env)
		if err != nil {
			log.LogAttrs(tempCtx, slog.LevelError, "Error creating deployment", slog.Any("error", err))
			http.Error(w, "Error generating deployment spec", http.StatusInternalServerError)
			return
		}
		log.DebugContext(tempCtx, "Applying deployment spec")
		newDeployment, err := kubernetes.CreateDeployment(config.App, deploymentSpec, env)
		if err != nil {
			log.LogAttrs(tempCtx, slog.LevelError, "Error creating deployment", slog.Any("error", err))
			http.Error(w, "Error creating deployment", http.StatusInternalServerError)
			return
		}
		tempCtx = context.WithValue(tempCtx, "deployment", newDeployment.Name)
		log.LogAttrs(
			tempCtx, slog.LevelDebug, "Successfully created deployment",
			slog.String("deployment", newDeployment.Name),
		)

		// --- Service ---
		log.DebugContext(tempCtx, "Creating service for deployment")
		svc, svcExists := serviceMap[service.Name]
		log.DebugContext(tempCtx, "Generating service spec")
		serviceSpec, err := kubernetes.GenerateServiceSpec(config.App, &service, svc)
		if err != nil {
			log.LogAttrs(tempCtx, slog.LevelError, "Error creating service spec", slog.Any("error", err))
			http.Error(w, "Error generating service spec", http.StatusInternalServerError)
			return
		}
		log.DebugContext(tempCtx, "Applying service spec")
		newService, err := kubernetes.CreateService(config.App, serviceSpec, env)
		if err != nil {
			log.LogAttrs(tempCtx, slog.LevelError, "Error apply service spec", slog.Any("error", err))
			http.Error(w, "Error creating service", http.StatusInternalServerError)
			return
		}
		log.DebugContext(tempCtx, "Successfully created service")
		serviceUrls[service.Name] = make([]string, 0)
		if svcExists && service.Template != "http" {
			// TODO: only run this if the service node ports changed
			tempCtx = context.WithValue(tempCtx, "service", newService.Name)
			log.DebugContext(tempCtx, "Updating service in database")
			var nodePorts []int32
			log.DebugContext(tempCtx, "Retrieving node ports from spec")
			for _, port := range newService.Spec.Ports {
				log.LogAttrs(tempCtx, slog.LevelDebug, "Node port", slog.Int("port", int(port.NodePort)))
				nodePorts = append(nodePorts, port.NodePort)
				serviceUrls[service.Name] = append(serviceUrls[service.Name], fmt.Sprintf("%s:%d", env.Getenv("DOMAIN"), port.NodePort))
			}

			log.DebugContext(tempCtx, "Updating node ports in database")
			err = db.SetServiceNodePorts(r.Context(), database.SetServiceNodePortsParams{
				Name:        newService.Name,
				ProjectName: project.Name,
				NodePorts:   nodePorts,
			})
			if err != nil {
				log.LogAttrs(tempCtx, slog.LevelError, "Error updating service in database", slog.Any("error", err))
				http.Error(w, "Error updating service", http.StatusInternalServerError)
				return
			}
		} else if !svcExists {
			log.DebugContext(tempCtx, "Creating service in database")
			var nodePorts []int32
			if service.Template != "http" {
				log.DebugContext(tempCtx, "Retrieving node ports from spec")
				for _, port := range newService.Spec.Ports {
					nodePorts = append(nodePorts, port.NodePort)
					serviceUrls[service.Name] = append(serviceUrls[service.Name], fmt.Sprintf("%s:%d", env.Getenv("DOMAIN"), port.NodePort))
				}
			}

			log.DebugContext(tempCtx, "Creating service in database")
			_, err = db.CreateService(r.Context(), database.CreateServiceParams{
				Name:        newService.Name,
				ProjectName: project.Name,
				NodePorts:   nodePorts,
			})

			if err != nil {
				log.LogAttrs(tempCtx, slog.LevelError, "Error creating service in database", slog.Any("error", err))
				http.Error(w, "Error creating service", http.StatusInternalServerError)
				return
			}
			svcExists = true
		}

		// --- Ingress ---
		if service.Template == "http" {
			log.DebugContext(tempCtx, "Creating ingress for service")
			log.DebugContext(tempCtx, "Generating ingress spec")
			ingressSpec, err := kubernetes.GenerateIngressSpec(config.App, &service, svc, env)
			if err != nil {
				log.LogAttrs(tempCtx, slog.LevelError, "Error generating ingress spec", slog.Any("error", err))
				http.Error(w, "Error generating ingress spec", http.StatusInternalServerError)
				return
			}
			log.DebugContext(tempCtx, "Applying ingress spec")
			newIngress, err := kubernetes.CreateIngress(config.App, ingressSpec, env)
			if err != nil {
				log.LogAttrs(tempCtx, slog.LevelError, "Error applying ingress spec", slog.Any("error", err))
				http.Error(w, "Error creating ingress", http.StatusInternalServerError)
				return
			}
			tempCtx = context.WithValue(tempCtx, "ingress", newIngress.Name)
			tempCtx = context.WithValue(tempCtx, "host", newIngress.Spec.Rules[0].Host)
			log.LogAttrs(tempCtx, slog.LevelDebug, "Successfully created ingress")

			if svcExists {
				log.DebugContext(tempCtx, "Updating ingress in database")
				err = db.SetServiceIngress(r.Context(), database.SetServiceIngressParams{
					Name:        service.Name,
					ProjectName: project.Name,
					Ingress:     pgtype.Text{String: newIngress.Spec.Rules[0].Host, Valid: true},
				})
				if err != nil {
					log.LogAttrs(tempCtx, slog.LevelError, "Error updating ingress in database", slog.Any("error", err))
					http.Error(w, "Error updating ingress", http.StatusInternalServerError)
					return
				}
			} else {
				log.DebugContext(tempCtx, "Creating ingress in database")
				_, err = db.CreateService(r.Context(), database.CreateServiceParams{
					Name:        service.Name,
					ProjectName: project.Name,
					Ingress:     pgtype.Text{String: newIngress.Spec.Rules[0].Host, Valid: true},
				})
				if err != nil {
					log.LogAttrs(tempCtx, slog.LevelError, "Error creating ingress in database", slog.Any("error", err))
					http.Error(w, "Error creating ingress", http.StatusInternalServerError)
					return
				}
			}
			serviceUrls[service.Name] = append(serviceUrls[service.Name], fmt.Sprintf("https://%s", newIngress.Spec.Rules[0].Host))
		}
	}

	log.DebugContext(ctx, "Deployment completed successfully")
	log.DebugContext(ctx, "Encoding response")
	json.NewEncoder(w).Encode(deployResponse{
		Urls: serviceUrls,
	})
}
