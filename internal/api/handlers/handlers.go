package handlers

import (
	"nimbus/internal/database"
	nimbusEnv "nimbus/internal/env"
	"nimbus/internal/kubernetes"
	"nimbus/internal/logging"
	"nimbus/internal/models"
	"nimbus/internal/utils"

	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"

	"github.com/google/uuid"

	"github.com/jackc/pgx/v5/pgtype"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
)

const formFile = "file"
const formBranch = "branch"
const xApiKey = "X-API-Key"

const envKey = "env"

func deleteRemovedServices(
	env *nimbusEnv.Env,
	ctx context.Context,
	w http.ResponseWriter,
) error {
	env.DebugContext(ctx, "Processing services in config file")
	serviceNames := make(map[string]bool)
	for _, service := range env.ProjectConfig.Services {
		env.LogAttrs(ctx, slog.LevelDebug, "Processing service", slog.String("service", service.Name))
		if serviceNames[service.Name] {
			env.LogAttrs(ctx, slog.LevelError, "Service names must be unique", slog.String("service", service.Name))
			http.Error(w, "Service names must be unique, duplicate of "+service.Name, http.StatusBadRequest)
			return nil
		}
		serviceNames[service.Name] = true
	}

	env.DebugContext(ctx, "Deleting services not in config file")
	for _, service := range env.ExistingServices {
		env.LogAttrs(ctx, slog.LevelDebug, "Processing service", slog.String("service", service.ServiceName))

		if _, ok := serviceNames[service.ServiceName]; !ok {
			env.LogAttrs(ctx, slog.LevelDebug, "Deleting deployment", slog.String("service", service.ServiceName))
			err := kubernetes.DeleteDeployment(env.Namespace, service.ServiceName, env)
			if err != nil {
				env.LogAttrs(
					ctx, slog.LevelError, "Error deleting deployment",
					slog.String("service", service.ServiceName), slog.Any("error", err),
				)
				http.Error(w, "Error deleting deployment", http.StatusInternalServerError)
				return err
			}

			env.LogAttrs(ctx, slog.LevelDebug, "Deleting service", slog.String("service", service.ServiceName))
			err = kubernetes.DeleteService(env.Namespace, service.ServiceName, env)
			if err != nil {
				env.LogAttrs(
					ctx, slog.LevelError, "Error deleting service",
					slog.String("service", service.ServiceName), slog.Any("error", err),
				)
				http.Error(w, "Error deleting service", http.StatusInternalServerError)
				return err
			}

			if service.Ingress.Valid {
				env.LogAttrs(ctx, slog.LevelDebug, "Deleting ingress", slog.String("service", service.ServiceName))
				err = kubernetes.DeleteIngress(env.Namespace, service.Ingress.String, env)
				if err != nil {
					env.LogAttrs(
						ctx, slog.LevelError, "Error deleting ingress",
						slog.String("service", service.ServiceName), slog.Any("error", err),
					)
					http.Error(w, "Error deleting ingress", http.StatusInternalServerError)
					return err
				}
			}

			env.LogAttrs(ctx, slog.LevelDebug, "Deleting service in database", slog.String("service", service.ServiceName))
			err = env.Database.DeleteServiceById(ctx, service.ID)
			if err != nil {
				env.LogAttrs(
					ctx, slog.LevelError, "Error deleting service in databse",
					slog.String("service", service.ServiceName), slog.Any("error", err),
				)
				http.Error(w, "Error deleting service", http.StatusInternalServerError)
				return err
			}
		}
	}
	return nil
}

func createDeployment(
	service models.Service,
	w http.ResponseWriter,
	env *nimbusEnv.Env,
	ctx context.Context,
) (string, error) {
	env.DebugContext(ctx, "Generating deployment spec")
	deploymentSpec, err := kubernetes.GenerateDeploymentSpec(env.Namespace, &service, env)
	if err != nil {
		env.LogAttrs(ctx, slog.LevelError, "Error creating deployment", slog.Any("error", err))
		http.Error(w, "Error generating deployment spec", http.StatusInternalServerError)
		return "", err
	}
	env.DebugContext(ctx, "Applying deployment spec")
	deployment, err := kubernetes.CreateDeployment(env.Namespace, deploymentSpec, env)
	if err != nil {
		env.LogAttrs(ctx, slog.LevelError, "Error creating deployment", slog.Any("error", err))
		http.Error(w, "Error creating deployment", http.StatusInternalServerError)
		return "", err
	}
	return deployment.Name, nil
}

func createService(
	newService *models.Service,
	oldService *database.Service,
	w http.ResponseWriter,
	env *nimbusEnv.Env,
	ctx context.Context,
) (*corev1.Service, error) {
	env.DebugContext(ctx, "Generating service spec")
	serviceSpec, err := kubernetes.GenerateServiceSpec(env.Namespace, newService, oldService)
	if err != nil {
		env.LogAttrs(ctx, slog.LevelError, "Error creating service spec", slog.Any("error", err))
		http.Error(w, "Error generating service spec", http.StatusInternalServerError)
		return nil, err
	}

	env.DebugContext(ctx, "Applying service spec")
	kubeSvc, err := kubernetes.CreateService(env.Namespace, serviceSpec, env)
	if err != nil {
		env.LogAttrs(ctx, slog.LevelError, "Error apply service spec", slog.Any("error", err))
		http.Error(w, "Error creating service", http.StatusInternalServerError)
		return nil, err
	}

	return kubeSvc, nil
}

func createDBService(
	service *corev1.Service,
	template string,
	w http.ResponseWriter,
	env *nimbusEnv.Env,
	ctx context.Context,
) ([]string, error) {
	serviceUrls := make([]string, 0)

	var nodePorts []int32
	if template != "http" {
		env.DebugContext(ctx, "Retrieving node ports from spec")
		for _, port := range service.Spec.Ports {
			nodePorts = append(nodePorts, port.NodePort)
			serviceUrls = append(serviceUrls, utils.FormatServiceURL(os.Getenv("DOMAIN"), port.NodePort))
		}
	}

	env.DebugContext(ctx, "Inserting service into database")
	_, err := env.Database.CreateService(ctx, database.CreateServiceParams{
		ID:            uuid.New(),
		ProjectID:     env.ProjectID,
		ProjectBranch: env.BranchName,
		ServiceName:   service.Name,
		NodePorts:     nodePorts,
	})

	if err != nil {
		env.LogAttrs(ctx, slog.LevelError, "Error creating service in database", slog.Any("error", err))
		http.Error(w, "Error creating service", http.StatusInternalServerError)
		return nil, err
	}

	return serviceUrls, nil
}

func updateDBService(
	serviceID uuid.UUID,
	service *corev1.Service,
	w http.ResponseWriter,
	env *nimbusEnv.Env,
	ctx context.Context,
) ([]string, error) {
	serviceUrls := make([]string, 0)
	env.DebugContext(ctx, "Updating service in database")

	// TODO: only run this if the service node ports changed
	var nodePorts []int32
	env.DebugContext(ctx, "Retrieving node ports from spec")
	for _, port := range service.Spec.Ports {
		env.LogAttrs(ctx, slog.LevelDebug, "Node port", slog.Int("port", int(port.NodePort)))
		nodePorts = append(nodePorts, port.NodePort)
		serviceUrls = append(serviceUrls, utils.FormatServiceURL(os.Getenv("DOMAIN"), port.NodePort))
	}

	env.DebugContext(ctx, "Updating row in database")
	err := env.Database.SetServiceNodePorts(ctx, database.SetServiceNodePortsParams{
		ID:        serviceID,
		NodePorts: nodePorts,
	})

	if err != nil {
		env.LogAttrs(ctx, slog.LevelError, "Error updating service in database", slog.Any("error", err))
		http.Error(w, "Error updating service", http.StatusInternalServerError)
		return nil, err
	}
	return serviceUrls, nil
}

func createIngress(
	newService models.Service,
	oldService *database.Service,
	w http.ResponseWriter,
	env *nimbusEnv.Env,
	ctx context.Context,
) (*networkingv1.Ingress, error) {
	env.DebugContext(ctx, "Generating ingress spec")
	ingressSpec, err := kubernetes.GenerateIngressSpec(env.Namespace, &newService, oldService, env)
	if err != nil {
		env.LogAttrs(ctx, slog.LevelError, "Error generating ingress spec", slog.Any("error", err))
		http.Error(w, "Error generating ingress spec", http.StatusInternalServerError)
		return nil, err
	}
	env.DebugContext(ctx, "Applying ingress spec")
	newIngress, err := kubernetes.CreateIngress(env.Namespace, ingressSpec, env)
	if err != nil {
		env.LogAttrs(ctx, slog.LevelError, "Error applying ingress spec", slog.Any("error", err))
		http.Error(w, "Error creating ingress", http.StatusInternalServerError)
		return nil, err
	}

	return newIngress, nil
}

func Deploy(w http.ResponseWriter, r *http.Request) {
	env, ok := r.Context().Value(envKey).(*nimbusEnv.Env)
	if !ok {
		env = nimbusEnv.Null()
	}

	log := env.Logger
	ctx := r.Context()

	deployRequest, ctx, err := buildDeployRequest(w, r, env, ctx)
	if err != nil {
		log.LogAttrs(ctx, slog.LevelError, "Error building deploy request", slog.Any("error", err))
		http.Error(w, "Error building deploy request", http.StatusBadRequest)
		return
	}
	env.DeployRequest = deployRequest

	log.DebugContext(ctx, "Ensuring namespace")
	err = kubernetes.EnsureNamespace(deployRequest.Namespace, env, ctx)
	if err != nil {
		log.LogAttrs(ctx, slog.LevelError, "Error ensuring namespace", slog.Any("error", err))
		http.Error(w, "Error ensuring namespace", http.StatusInternalServerError)
		return
	}
	ctx = logging.AppendCtx(ctx, slog.String("namespace", deployRequest.Namespace))

	err = deleteRemovedServices(env, ctx, w)
	if err != nil {
		log.LogAttrs(ctx, slog.LevelError, "Error deleting removed services", slog.Any("error", err))
		http.Error(w, "Error deleting removed services", http.StatusInternalServerError)
		return
	}

	log.DebugContext(ctx, "Creating services and deployments")
	serviceUrls := make(map[string][]string)
	log.DebugContext(ctx, "Creating service map for existing services")
	var existingServices = make(map[string]*database.Service)
	for _, service := range deployRequest.ExistingServices {
		existingServices[service.ServiceName] = &service
	}

	for _, newService := range deployRequest.ProjectConfig.Services {
		tempCtx := logging.AppendCtx(ctx, slog.String("service", newService.Name))

		// Create deployment
		env.DebugContext(ctx, "Creating deployment")
		name, err := createDeployment(newService, w, env, ctx)
		if err != nil {
			log.LogAttrs(tempCtx, slog.LevelError, "Error creating deployment", slog.Any("error", err))
			http.Error(w, "Error creating deployment", http.StatusInternalServerError)
			return
		}
		env.LogAttrs(tempCtx, slog.LevelDebug, "Successfully created deployment", slog.String("deployment", name))

		// Create service
		log.DebugContext(tempCtx, "Creating service for deployment")
		oldService, svcExists := existingServices[newService.Name]
		kubeSvc, err := createService(&newService, oldService, w, env, ctx)
		if err != nil {
			log.LogAttrs(tempCtx, slog.LevelError, "Error creating service", slog.Any("error", err))
			http.Error(w, "Error creating service", http.StatusInternalServerError)
			return
		}

		urls := make([]string, 0)
		if svcExists && newService.Template != "http" {
			env.DebugContext(tempCtx, "Updating Service in database")
			urls, err = updateDBService(oldService.ID, kubeSvc, w, env, tempCtx)
		} else if !svcExists {
			env.DebugContext(tempCtx, "Creating Service in database")
			urls, err = createDBService(kubeSvc, newService.Template, w, env, tempCtx)
		}
		if err != nil {
			env.LogAttrs(tempCtx, slog.LevelError, "Error creating service in database", slog.Any("error", err))
			http.Error(w, "Error creating service in database", http.StatusInternalServerError)
			return
		}
		env.DebugContext(tempCtx, "Successfully created service")
		svcExists = true
		serviceUrls[newService.Name] = urls

		// Create ingress
		if newService.Template == "http" {
			log.DebugContext(tempCtx, "Creating ingress for service")
			newIngress, err := createIngress(newService, oldService, w, env, tempCtx)
			if err != nil {
				return
			}
			log.LogAttrs(tempCtx, slog.LevelDebug, "Successfully created ingress")

			if svcExists {
				log.DebugContext(tempCtx, "Updating ingress in database")
				err = env.Database.SetServiceIngress(r.Context(), database.SetServiceIngressParams{
					ID:      oldService.ID,
					Ingress: pgtype.Text{String: newIngress.Spec.Rules[0].Host, Valid: true},
				})
				if err != nil {
					log.LogAttrs(tempCtx, slog.LevelError, "Error updating ingress in database", slog.Any("error", err))
					http.Error(w, "Error updating ingress", http.StatusInternalServerError)
					return
				}
			} else {
				log.DebugContext(tempCtx, "Creating ingress in database")
				_, err = env.Database.CreateService(r.Context(), database.CreateServiceParams{
					ID:            uuid.New(),
					ProjectID:     deployRequest.ProjectID,
					ProjectBranch: deployRequest.BranchName,
					ServiceName:   newService.Name,
					Ingress:       pgtype.Text{String: newIngress.Spec.Rules[0].Host, Valid: true},
				})
				if err != nil {
					log.LogAttrs(tempCtx, slog.LevelError, "Error creating ingress in database", slog.Any("error", err))
					http.Error(w, "Error creating ingress", http.StatusInternalServerError)
					return
				}
			}
			serviceUrls[newService.Name] = append(serviceUrls[newService.Name], fmt.Sprintf("https://%s", newIngress.Spec.Rules[0].Host))
		}
	}

	log.DebugContext(ctx, "Deployment completed successfully")
	log.DebugContext(ctx, "Encoding response")
	json.NewEncoder(w).Encode(deployResponse{
		Urls: serviceUrls,
	})
}
