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
	service *models.Service,
	w http.ResponseWriter,
	env *nimbusEnv.Env,
	ctx context.Context,
) (string, error) {
	env.DebugContext(ctx, "Generating deployment spec")
	deploymentSpec, err := kubernetes.GenerateDeploymentSpec(env.Namespace, service, env)
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
	serviceConfig *models.Service,
	oldService *database.Service,
	w http.ResponseWriter,
	env *nimbusEnv.Env,
	ctx context.Context,
) (*corev1.Service, error) {
	env.DebugContext(ctx, "Generating service spec")
	serviceSpec, err := kubernetes.GenerateServiceSpec(env.Namespace, serviceConfig, oldService)
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
	name string,
	kubeSvc *corev1.Service,
	w http.ResponseWriter,
	env *nimbusEnv.Env,
	ctx context.Context,
) (*database.Service, error) {
	env.DebugContext(ctx, "Inserting service into database")
	newSvc, err := env.Database.CreateService(ctx, database.CreateServiceParams{
		ID:            uuid.New(),
		ProjectID:     env.ProjectID,
		ProjectBranch: env.BranchName,
		ServiceName:   name,
	})

	if err != nil {
		env.LogAttrs(ctx, slog.LevelError, "Error creating service in database", slog.Any("error", err))
		http.Error(w, "Error creating service", http.StatusInternalServerError)
		return nil, err
	}

	return &newSvc, nil
}

func updateDBService(
	serviceID uuid.UUID,
	existingIngress *string,
	serviceConfig *models.Service,
	kubeSvc *corev1.Service,
	w http.ResponseWriter,
	env *nimbusEnv.Env,
	ctx context.Context,
) ([]string, error) {
	serviceUrls := make([]string, 0)

	if serviceConfig.Template != "http" {
		// TODO: only run this if the service node ports changed
		var nodePorts []int32
		env.DebugContext(ctx, "Retrieving node ports from spec")
		for _, port := range kubeSvc.Spec.Ports {
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
	} else {
		env.DebugContext(ctx, "Creating ingress for service")
		newIngress, err := createIngress(serviceConfig, existingIngress, w, env, ctx)
		if err != nil {
			return nil, err
		}
		env.LogAttrs(ctx, slog.LevelDebug, "Successfully created ingress")

		env.DebugContext(ctx, "Updating ingress in database")
		err = env.Database.SetServiceIngress(ctx, database.SetServiceIngressParams{
			ID:      serviceID,
			Ingress: pgtype.Text{String: newIngress.Spec.Rules[0].Host, Valid: true},
		})
		if err != nil {
			env.LogAttrs(ctx, slog.LevelError, "Error updating ingress in database", slog.Any("error", err))
			http.Error(w, "Error updating ingress", http.StatusInternalServerError)
			return nil, err
		}
		serviceUrls = append(serviceUrls, fmt.Sprintf("https://%s", newIngress.Spec.Rules[0].Host))
	}

	return serviceUrls, nil
}

func createIngress(
	serviceConfig *models.Service,
	existingIngress *string,
	w http.ResponseWriter,
	env *nimbusEnv.Env,
	ctx context.Context,
) (*networkingv1.Ingress, error) {
	env.DebugContext(ctx, "Generating ingress spec")
	ingressSpec, err := kubernetes.GenerateIngressSpec(env.Namespace, serviceConfig, existingIngress, env)
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

	ctx := r.Context()

	deployRequest, ctx, err := buildDeployRequest(w, r, env, ctx)
	if err != nil {
		env.LogAttrs(ctx, slog.LevelError, "Error building deploy request", slog.Any("error", err))
		http.Error(w, "Error building deploy request", http.StatusBadRequest)
		return
	}
	env.DeployRequest = deployRequest

	env.DebugContext(ctx, "Ensuring namespace")
	err = kubernetes.EnsureNamespace(deployRequest.Namespace, env, ctx)
	if err != nil {
		env.LogAttrs(ctx, slog.LevelError, "Error ensuring namespace", slog.Any("error", err))
		http.Error(w, "Error ensuring namespace", http.StatusInternalServerError)
		return
	}
	ctx = logging.AppendCtx(ctx, slog.String("namespace", deployRequest.Namespace))

	err = deleteRemovedServices(env, ctx, w)
	if err != nil {
		env.LogAttrs(ctx, slog.LevelError, "Error deleting removed services", slog.Any("error", err))
		http.Error(w, "Error deleting removed services", http.StatusInternalServerError)
		return
	}

	env.DebugContext(ctx, "Creating services and deployments")
	serviceUrls := make(map[string][]string)
	env.DebugContext(ctx, "Creating service map for existing services")
	var existingServices = make(map[string]*database.Service)
	for _, service := range deployRequest.ExistingServices {
		existingServices[service.ServiceName] = &service
	}

	for _, serviceConfig := range deployRequest.ProjectConfig.Services {
		tempCtx := logging.AppendCtx(ctx, slog.String("service", serviceConfig.Name))

		// Create deployment
		env.DebugContext(ctx, "Creating Kubernetes deployment")
		name, err := createDeployment(&serviceConfig, w, env, ctx)
		if err != nil {
			env.LogAttrs(tempCtx, slog.LevelError, "Error creating Kubernetes deployment", slog.Any("error", err))
			http.Error(w, "Error creating Kubernetes deployment", http.StatusInternalServerError)
			return
		}
		env.LogAttrs(tempCtx, slog.LevelDebug, "Successfully created Kubernetes deployment", slog.String("deployment", name))

		// Create service
		env.DebugContext(tempCtx, "Creating Kubernetes service")
		oldService, svcExists := existingServices[serviceConfig.Name]
		kubeSvc, err := createService(&serviceConfig, oldService, w, env, ctx)
		if err != nil {
			env.LogAttrs(tempCtx, slog.LevelError, "Error creating Kubernetes service", slog.Any("error", err))
			http.Error(w, "Error creating Kubernetes service", http.StatusInternalServerError)
			return
		}

		urls := make([]string, 0)
		var newSvc *database.Service
		if !svcExists {
			env.DebugContext(tempCtx, "Creating service in database")
			newSvc, err = createDBService(serviceConfig.Name, kubeSvc, w, env, tempCtx)
			if err != nil {
				env.LogAttrs(tempCtx, slog.LevelError, "Error creating service in database", slog.Any("error", err))
				http.Error(w, "Error creating service in database", http.StatusInternalServerError)
				return
			}
		}
		var serviceID uuid.UUID
		var existingIngress *string
		if svcExists {
			serviceID = oldService.ID
			if oldService.Ingress.Valid {
				existingIngress = &oldService.Ingress.String
			}
		} else {
			serviceID = newSvc.ID
		}
		env.DebugContext(tempCtx, "Updating service networking in database")
		urls, err = updateDBService(serviceID, existingIngress, &serviceConfig, kubeSvc, w, env, tempCtx)
		if err != nil {
			env.LogAttrs(tempCtx, slog.LevelError, "Error updating service networking in database", slog.Any("error", err))
			http.Error(w, "Error updating service networking in database", http.StatusInternalServerError)
			return
		}
		env.DebugContext(tempCtx, "Successfully created service in database")
		serviceUrls[serviceConfig.Name] = urls
	}

	env.DebugContext(ctx, "Deployment completed successfully")
	env.DebugContext(ctx, "Encoding response")
	json.NewEncoder(w).Encode(deployResponse{
		Urls: serviceUrls,
	})
}
