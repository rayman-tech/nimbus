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
	"strings"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
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
	env.Logger.DebugContext(ctx, "Processing services in config file")
	serviceNames := make(map[string]bool)
	for _, service := range env.Deployment.ProjectConfig.Services {
		env.Logger.LogAttrs(ctx, slog.LevelDebug, "Processing service", slog.String("service", service.Name))
		if serviceNames[service.Name] {
			env.Logger.LogAttrs(ctx, slog.LevelError, "Service names must be unique", slog.String("service", service.Name))
			http.Error(w, "Service names must be unique, duplicate of "+service.Name, http.StatusBadRequest)
			return nil
		}
		serviceNames[service.Name] = true
	}

	env.Logger.DebugContext(ctx, "Deleting services not in config file")
	for _, service := range env.Deployment.ExistingServices {
		env.Logger.LogAttrs(ctx, slog.LevelDebug, "Processing service", slog.String("service", service.ServiceName))

		if _, ok := serviceNames[service.ServiceName]; !ok {
			env.Logger.LogAttrs(ctx, slog.LevelDebug, "Deleting deployment", slog.String("service", service.ServiceName))
			err := kubernetes.DeleteDeployment(env.Deployment.Namespace, service.ServiceName, env)
			if err != nil {
				env.Logger.LogAttrs(
					ctx, slog.LevelError, "Error deleting deployment",
					slog.String("service", service.ServiceName), slog.Any("error", err),
				)
				http.Error(w, "Error deleting deployment", http.StatusInternalServerError)
				return err
			}

			env.Logger.LogAttrs(ctx, slog.LevelDebug, "Deleting service", slog.String("service", service.ServiceName))
			err = kubernetes.DeleteService(env.Deployment.Namespace, service.ServiceName, env)
			if err != nil {
				env.Logger.LogAttrs(
					ctx, slog.LevelError, "Error deleting service",
					slog.String("service", service.ServiceName), slog.Any("error", err),
				)
				http.Error(w, "Error deleting service", http.StatusInternalServerError)
				return err
			}

			if service.Ingress.Valid {
				env.Logger.LogAttrs(ctx, slog.LevelDebug, "Deleting ingress", slog.String("service", service.ServiceName))
				err = kubernetes.DeleteIngress(env.Deployment.Namespace, service.Ingress.String, env)
				if err != nil {
					env.Logger.LogAttrs(
						ctx, slog.LevelError, "Error deleting ingress",
						slog.String("service", service.ServiceName), slog.Any("error", err),
					)
					http.Error(w, "Error deleting ingress", http.StatusInternalServerError)
					return err
				}
			}

			env.Logger.LogAttrs(ctx, slog.LevelDebug, "Deleting service in database", slog.String("service", service.ServiceName))
			err = env.Database.DeleteServiceById(ctx, service.ID)
			if err != nil {
				env.Logger.LogAttrs(
					ctx, slog.LevelError, "Error deleting service in database",
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
	env.Logger.DebugContext(ctx, "Generating deployment spec")
	deploymentSpec, err := kubernetes.GenerateDeploymentSpec(env.Deployment.Namespace, service, env)
	if err != nil {
		env.Logger.LogAttrs(ctx, slog.LevelError, "Error creating deployment", slog.Any("error", err))
		http.Error(w, "Error generating deployment spec", http.StatusInternalServerError)
		return "", err
	}
	env.Logger.DebugContext(ctx, "Applying deployment spec")
	deployment, err := kubernetes.CreateDeployment(env.Deployment.Namespace, deploymentSpec, env)
	if err != nil {
		env.Logger.LogAttrs(ctx, slog.LevelError, "Error creating deployment", slog.Any("error", err))
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
	env.Logger.DebugContext(ctx, "Generating service spec")
	serviceSpec, err := kubernetes.GenerateServiceSpec(env.Deployment.Namespace, serviceConfig, oldService)
	if err != nil {
		env.Logger.LogAttrs(ctx, slog.LevelError, "Error creating service spec", slog.Any("error", err))
		http.Error(w, "Error generating service spec", http.StatusInternalServerError)
		return nil, err
	}

	env.Logger.DebugContext(ctx, "Applying service spec")
	kubeSvc, err := kubernetes.CreateService(env.Deployment.Namespace, serviceSpec, env)
	if err != nil {
		env.Logger.LogAttrs(ctx, slog.LevelError, "Error apply service spec", slog.Any("error", err))
		http.Error(w, "Error creating service", http.StatusInternalServerError)
		return nil, err
	}

	return kubeSvc, nil
}

func createDBService(
	name string,
	_ *corev1.Service,
	w http.ResponseWriter,
	env *nimbusEnv.Env,
	ctx context.Context,
) (*database.Service, error) {
	env.Logger.DebugContext(ctx, "Inserting service into database")
	newSvc, err := env.Database.CreateService(ctx, database.CreateServiceParams{
		ID:            uuid.New(),
		ProjectID:     env.Deployment.ProjectID,
		ProjectBranch: env.Deployment.BranchName,
		ServiceName:   name,
	})

	if err != nil {
		env.Logger.LogAttrs(ctx, slog.LevelError, "Error creating service in database", slog.Any("error", err))
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
		if !serviceConfig.Public {
			env.Logger.DebugContext(ctx, "Clearing node ports for private service")
			err := env.Database.SetServiceNodePorts(ctx, database.SetServiceNodePortsParams{
				ID:        serviceID,
				NodePorts: []int32{},
			})
			if err != nil {
				env.Logger.LogAttrs(ctx, slog.LevelError, "Error updating service in database", slog.Any("error", err))
				http.Error(w, "Error updating service", http.StatusInternalServerError)
				return nil, err
			}
			return serviceUrls, nil
		}

		// TODO: only run this if the service node ports changed
		var nodePorts []int32
		env.Logger.DebugContext(ctx, "Retrieving node ports from spec")
		for _, port := range kubeSvc.Spec.Ports {
			env.Logger.LogAttrs(ctx, slog.LevelDebug, "Node port", slog.Int("port", int(port.NodePort)))
			nodePorts = append(nodePorts, port.NodePort)
			serviceUrls = append(serviceUrls, utils.FormatServiceURL(os.Getenv("DOMAIN"), port.NodePort))
		}

		env.Logger.DebugContext(ctx, "Updating row in database")
		err := env.Database.SetServiceNodePorts(ctx, database.SetServiceNodePortsParams{
			ID:        serviceID,
			NodePorts: nodePorts,
		})

		if err != nil {
			env.Logger.LogAttrs(ctx, slog.LevelError, "Error updating service in database", slog.Any("error", err))
			http.Error(w, "Error updating service", http.StatusInternalServerError)
			return nil, err
		}
	} else {
		if !serviceConfig.Public {
			if existingIngress != nil {
				env.Logger.DebugContext(ctx, "Deleting existing ingress for private service")
				err := kubernetes.DeleteIngress(env.Deployment.Namespace, *existingIngress, env)
				if err != nil {
					env.Logger.LogAttrs(ctx, slog.LevelError, "Error deleting ingress", slog.Any("error", err))
					http.Error(w, "Error deleting ingress", http.StatusInternalServerError)
					return nil, err
				}
			}
			err := env.Database.SetServiceIngress(ctx, database.SetServiceIngressParams{
				ID:      serviceID,
				Ingress: pgtype.Text{Valid: false},
			})
			if err != nil {
				env.Logger.LogAttrs(ctx, slog.LevelError, "Error updating ingress in database", slog.Any("error", err))
				http.Error(w, "Error updating ingress", http.StatusInternalServerError)
				return nil, err
			}
			return serviceUrls, nil
		}

		env.Logger.DebugContext(ctx, "Creating ingress for service")
		newIngress, err := createIngress(serviceConfig, existingIngress, w, env, ctx)
		if err != nil {
			return nil, err
		}
		env.Logger.LogAttrs(ctx, slog.LevelDebug, "Successfully created ingress")

		env.Logger.DebugContext(ctx, "Updating ingress in database")
		err = env.Database.SetServiceIngress(ctx, database.SetServiceIngressParams{
			ID:      serviceID,
			Ingress: pgtype.Text{String: newIngress.Spec.Rules[0].Host, Valid: true},
		})
		if err != nil {
			env.Logger.LogAttrs(ctx, slog.LevelError, "Error updating ingress in database", slog.Any("error", err))
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
	env.Logger.DebugContext(ctx, "Generating ingress spec")
	ingressSpec, err := kubernetes.GenerateIngressSpec(env.Deployment.Namespace, serviceConfig, existingIngress, env)
	if err != nil {
		env.Logger.LogAttrs(ctx, slog.LevelError, "Error generating ingress spec", slog.Any("error", err))
		http.Error(w, "Error generating ingress spec", http.StatusInternalServerError)
		return nil, err
	}
	env.Logger.DebugContext(ctx, "Applying ingress spec")
	newIngress, err := kubernetes.CreateIngress(env.Deployment.Namespace, ingressSpec, env)
	if err != nil {
		env.Logger.LogAttrs(ctx, slog.LevelError, "Error applying ingress spec", slog.Any("error", err))
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
		env.Logger.LogAttrs(ctx, slog.LevelError, "Error building deploy request", slog.Any("error", err))
		http.Error(w, "Error building deploy request", http.StatusBadRequest)
		return
	}
	env.Deployment = deployRequest

	env.Logger.DebugContext(ctx, "Validating namespace")
	err = kubernetes.ValidateNamespace(deployRequest.Namespace, env, ctx)
	if err != nil {
		env.Logger.LogAttrs(ctx, slog.LevelError, "Error validating namespace", slog.Any("error", err))
		http.Error(w, "Error validating namespace", http.StatusInternalServerError)
		return
	}
	ctx = logging.AppendCtx(ctx, slog.String("namespace", deployRequest.Namespace))

	err = deleteRemovedServices(env, ctx, w)
	if err != nil {
		env.Logger.LogAttrs(ctx, slog.LevelError, "Error deleting removed services", slog.Any("error", err))
		http.Error(w, "Error deleting removed services", http.StatusInternalServerError)
		return
	}

	env.Logger.DebugContext(ctx, "Creating services and deployments")
	serviceUrls := make(map[string][]string)
	env.Logger.DebugContext(ctx, "Creating service map for existing services")
	var existingServices = make(map[string]*database.Service)
	for _, service := range deployRequest.ExistingServices {
		existingServices[service.ServiceName] = &service
	}

	for _, serviceConfig := range deployRequest.ProjectConfig.Services {
		env.Logger.LogAttrs(ctx, slog.LevelDebug, "Processing service", slog.Any("serviceConfig", serviceConfig))

		tempCtx := logging.AppendCtx(ctx, slog.String("service", serviceConfig.Name))

		// Create deployment
		env.Logger.DebugContext(ctx, "Creating Kubernetes deployment")
		name, err := createDeployment(&serviceConfig, w, env, ctx)
		if err != nil {
			env.Logger.LogAttrs(tempCtx, slog.LevelError, "Error creating Kubernetes deployment", slog.Any("error", err))
			http.Error(w, "Error creating Kubernetes deployment", http.StatusInternalServerError)
			return
		}
		env.Logger.LogAttrs(tempCtx, slog.LevelDebug, "Successfully created Kubernetes deployment", slog.String("deployment", name))

		// Create service
		env.Logger.DebugContext(tempCtx, "Creating Kubernetes service")
		oldService, svcExists := existingServices[serviceConfig.Name]
		kubeSvc, err := createService(&serviceConfig, oldService, w, env, ctx)
		if err != nil {
			env.Logger.LogAttrs(tempCtx, slog.LevelError, "Error creating Kubernetes service", slog.Any("error", err))
			http.Error(w, "Error creating Kubernetes service", http.StatusInternalServerError)
			return
		}

		urls := make([]string, 0)
		var newSvc *database.Service
		if !svcExists {
			env.Logger.DebugContext(tempCtx, "Creating service in database")
			newSvc, err = createDBService(serviceConfig.Name, kubeSvc, w, env, tempCtx)
			if err != nil {
				env.Logger.LogAttrs(tempCtx, slog.LevelError, "Error creating service in database", slog.Any("error", err))
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
		env.Logger.DebugContext(tempCtx, "Updating service networking in database")
		urls, err = updateDBService(serviceID, existingIngress, &serviceConfig, kubeSvc, w, env, tempCtx)
		if err != nil {
			env.Logger.LogAttrs(tempCtx, slog.LevelError, "Error updating service networking in database", slog.Any("error", err))
			http.Error(w, "Error updating service networking in database", http.StatusInternalServerError)
			return
		}
		env.Logger.DebugContext(tempCtx, "Successfully created service in database")
		serviceUrls[serviceConfig.Name] = urls
	}

	env.Logger.DebugContext(ctx, "Deployment completed successfully")
	env.Logger.DebugContext(ctx, "Encoding response")
	json.NewEncoder(w).Encode(deployResponse{
		Urls: serviceUrls,
	})
}

func CreateProject(w http.ResponseWriter, r *http.Request) {
	env, ok := r.Context().Value(envKey).(*nimbusEnv.Env)
	if !ok {
		env = nimbusEnv.Null()
	}

	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}

	apiKey := r.Header.Get(xApiKey)
	user, err := env.Database.GetUserByApiKey(r.Context(), apiKey)
	if err != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	project, err := env.Database.CreateProject(r.Context(), database.CreateProjectParams{
		ID:   uuid.New(),
		Name: req.Name,
	})
	if err != nil {
		http.Error(w, "error creating project", http.StatusInternalServerError)
		return
	}

	env.Database.AddUserToProject(r.Context(), database.AddUserToProjectParams{
		UserID:    user.ID,
		ProjectID: project.ID,
	})

	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(project)
}

func GetProjects(w http.ResponseWriter, r *http.Request) {
	env, ok := r.Context().Value(envKey).(*nimbusEnv.Env)
	if !ok {
		env = nimbusEnv.Null()
	}

	apiKey := r.Header.Get(xApiKey)
	user, err := env.Database.GetUserByApiKey(r.Context(), apiKey)
	if err != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	projects, err := env.Database.GetProjectsByUser(r.Context(), user.ID)
	if err != nil {
		http.Error(w, "error fetching projects", http.StatusInternalServerError)
		return
	}
	json.NewEncoder(w).Encode(projectsResponse{Projects: projects})
}

func GetServices(w http.ResponseWriter, r *http.Request) {
	env, ok := r.Context().Value(envKey).(*nimbusEnv.Env)
	if !ok {
		env = nimbusEnv.Null()
	}

	apiKey := r.Header.Get(xApiKey)
	user, err := env.Database.GetUserByApiKey(r.Context(), apiKey)
	if err != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	services, err := env.Database.GetServicesByUser(r.Context(), user.ID)
	if err != nil {
		http.Error(w, "error fetching services", http.StatusInternalServerError)
		return
	}

	items := make([]serviceListItem, 0, len(services))
	replacer := strings.NewReplacer("/", "-", "_", "-", " ", "-", "#", "", "!", "", "@", "", ".", "")
	for _, svc := range services {
		namespace := svc.ProjectName
		if svc.ProjectBranch != "main" && svc.ProjectBranch != "master" {
			namespace = fmt.Sprintf("%s-%s", svc.ProjectName, replacer.Replace(svc.ProjectBranch))
		}
		pods, err := kubernetes.GetPods(namespace, svc.ServiceName, env)
		status := "Unknown"
		if err == nil && len(pods) > 0 {
			status = string(pods[0].Status.Phase)
		}
		items = append(items, serviceListItem{
			ProjectName:   svc.ProjectName,
			ProjectBranch: svc.ProjectBranch,
			ServiceName:   svc.ServiceName,
			Status:        status,
		})
	}
	json.NewEncoder(w).Encode(servicesResponse{Services: items})
}

func GetService(w http.ResponseWriter, r *http.Request) {
	env, ok := r.Context().Value(envKey).(*nimbusEnv.Env)
	if !ok {
		env = nimbusEnv.Null()
	}

	vars := mux.Vars(r)
	name := vars["name"]
	projectName := r.URL.Query().Get("project")
	branch := r.URL.Query().Get("branch")
	if branch == "" {
		branch = "main"
	}

	apiKey := r.Header.Get(xApiKey)
	user, err := env.Database.GetUserByApiKey(r.Context(), apiKey)
	if err != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	project, err := env.Database.GetProjectByName(r.Context(), projectName)
	if err != nil {
		http.Error(w, "project not found", http.StatusNotFound)
		return
	}

	authorized, err := env.Database.IsUserInProject(r.Context(), database.IsUserInProjectParams{UserID: user.ID, ProjectID: project.ID})
	if err != nil || !authorized {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	svc, err := env.Database.GetServiceByName(r.Context(), database.GetServiceByNameParams{ServiceName: name, ProjectID: project.ID, ProjectBranch: branch})
	if err != nil {
		http.Error(w, "service not found", http.StatusNotFound)
		return
	}

	namespace := project.Name
	replacer := strings.NewReplacer("/", "-", "_", "-", " ", "-", "#", "", "!", "", "@", "", ".", "")
	if branch != "main" && branch != "master" {
		namespace = fmt.Sprintf("%s-%s", project.Name, replacer.Replace(branch))
	}

	pods, err := kubernetes.GetPods(namespace, name, env)
	if err != nil {
		http.Error(w, "error getting pods", http.StatusInternalServerError)
		return
	}

	podStatuses := make([]podStatus, 0)
	for _, p := range pods {
		podStatuses = append(podStatuses, podStatus{p.Name, string(p.Status.Phase)})
	}

	var ingress *string
	if svc.Ingress.Valid {
		ingress = &svc.Ingress.String
	}

	var logs string
	if len(pods) > 0 {
		data, err := kubernetes.GetPodLogsTail(namespace, pods[0].Name, 20, env)
		if err == nil {
			logs = string(data)
		}
	}

	resp := serviceDetailResponse{
		Project:     project.Name,
		Branch:      branch,
		Name:        name,
		NodePorts:   svc.NodePorts,
		Ingress:     ingress,
		PodStatuses: podStatuses,
		Logs:        logs,
	}
	json.NewEncoder(w).Encode(resp)
}

// StreamLogs streams logs for the first pod of a given service.
func StreamLogs(w http.ResponseWriter, r *http.Request) {
	env, ok := r.Context().Value(envKey).(*nimbusEnv.Env)
	if !ok {
		env = nimbusEnv.Null()
	}

	vars := mux.Vars(r)
	name := vars["name"]
	projectName := r.URL.Query().Get("project")
	branch := r.URL.Query().Get("branch")
	if branch == "" {
		branch = "main"
	}

	apiKey := r.Header.Get(xApiKey)
	user, err := env.Database.GetUserByApiKey(r.Context(), apiKey)
	if err != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	project, err := env.Database.GetProjectByName(r.Context(), projectName)
	if err != nil {
		http.Error(w, "project not found", http.StatusNotFound)
		return
	}

	authorized, err := env.Database.IsUserInProject(r.Context(), database.IsUserInProjectParams{UserID: user.ID, ProjectID: project.ID})
	if err != nil || !authorized {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	namespace := project.Name
	replacer := strings.NewReplacer("/", "-", "_", "-", " ", "-", "#", "", "!", "", "@", "", ".", "")
	if branch != "main" && branch != "master" {
		namespace = fmt.Sprintf("%s-%s", project.Name, replacer.Replace(branch))
	}

	stream, err := kubernetes.StreamServiceLogs(namespace, name, env)
	if err != nil {
		http.Error(w, "error streaming logs", http.StatusInternalServerError)
		return
	}
	defer stream.Close()

	w.Header().Set("Content-Type", "text/plain")

	flusher, _ := w.(http.Flusher)
	buf := make([]byte, 1024)
	for {
		n, err := stream.Read(buf)
		if n > 0 {
			w.Write(buf[:n])
			if flusher != nil {
				flusher.Flush()
			}
		}
		if err != nil {
			break
		}
	}
}

func DeleteBranch(w http.ResponseWriter, r *http.Request) {
	env, ok := r.Context().Value(envKey).(*nimbusEnv.Env)
	if !ok {
		env = nimbusEnv.Null()
	}

	projectName := r.URL.Query().Get("project")
	branch := r.URL.Query().Get("branch")
	if projectName == "" || branch == "" {
		http.Error(w, "missing project or branch", http.StatusBadRequest)
		return
	}

	apiKey := r.Header.Get(xApiKey)
	user, err := env.Database.GetUserByApiKey(r.Context(), apiKey)
	if err != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	project, err := env.Database.GetProjectByName(r.Context(), projectName)
	if err != nil {
		http.Error(w, "project not found", http.StatusNotFound)
		return
	}

	authorized, err := env.Database.IsUserInProject(r.Context(), database.IsUserInProjectParams{UserID: user.ID, ProjectID: project.ID})
	if err != nil || !authorized {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	services, err := env.Database.GetServicesByProject(r.Context(), database.GetServicesByProjectParams{ProjectID: project.ID, ProjectBranch: branch})
	if err != nil {
		env.Logger.ErrorContext(context.Background(), err.Error())
		http.Error(w, "error fetching services", http.StatusInternalServerError)
		return
	}

	namespace := project.Name
	replacer := strings.NewReplacer("/", "-", "_", "-", " ", "-", "#", "", "!", "", "@", "", ".", "")
	if branch != "main" && branch != "master" {
		namespace = fmt.Sprintf("%s-%s", project.Name, replacer.Replace(branch))
	}

	for _, svc := range services {
		kubernetes.DeleteDeployment(namespace, svc.ServiceName, env)
		kubernetes.DeleteService(namespace, svc.ServiceName, env)
		if svc.Ingress.Valid {
			kubernetes.DeleteIngress(namespace, svc.Ingress.String, env)
		}
		env.Database.DeleteServiceById(r.Context(), svc.ID)
	}

	ids, err := env.Database.GetUnusedVolumeIdentifiers(r.Context(), database.GetUnusedVolumeIdentifiersParams{ProjectID: project.ID, ProjectBranch: branch, Column3: []string{}})
	if err == nil {
		for _, id := range ids {
			kubernetes.DeletePVC(namespace, fmt.Sprintf("pvc-%s", id.String()), env)
		}
	}
	env.Database.DeleteUnusedVolumes(r.Context(), database.DeleteUnusedVolumesParams{ProjectID: project.ID, ProjectBranch: branch, Column3: []string{}})

	kubernetes.DeleteNamespace(namespace, env)
	w.WriteHeader(http.StatusOK)
}

func DeleteProject(w http.ResponseWriter, r *http.Request) {
	env, ok := r.Context().Value(envKey).(*nimbusEnv.Env)
	if !ok {
		env = nimbusEnv.Null()
	}

	vars := mux.Vars(r)
	projectName := vars["name"]
	if projectName == "" {
		http.Error(w, "missing project", http.StatusBadRequest)
		return
	}

	apiKey := r.Header.Get(xApiKey)
	user, err := env.Database.GetUserByApiKey(r.Context(), apiKey)
	if err != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	project, err := env.Database.GetProjectByName(r.Context(), projectName)
	if err != nil {
		http.Error(w, "project not found", http.StatusNotFound)
		return
	}

	authorized, err := env.Database.IsUserInProject(r.Context(), database.IsUserInProjectParams{UserID: user.ID, ProjectID: project.ID})
	if err != nil || !authorized {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	branches, err := env.Database.GetProjectBranches(r.Context(), project.ID)
	if err != nil {
		env.Logger.ErrorContext(context.Background(), err.Error())
		http.Error(w, "error fetching branches", http.StatusInternalServerError)
		return
	}

	replacer := strings.NewReplacer("/", "-", "_", "-", " ", "-", "#", "", "!", "", "@", "", ".", "")
	for _, branch := range branches {
		services, err := env.Database.GetServicesByProject(r.Context(), database.GetServicesByProjectParams{ProjectID: project.ID, ProjectBranch: branch})
		if err != nil {
			env.Logger.ErrorContext(context.Background(), err.Error())
			continue
		}

		namespace := project.Name
		if branch != "main" && branch != "master" {
			namespace = fmt.Sprintf("%s-%s", project.Name, replacer.Replace(branch))
		}

		for _, svc := range services {
			kubernetes.DeleteDeployment(namespace, svc.ServiceName, env)
			kubernetes.DeleteService(namespace, svc.ServiceName, env)
			if svc.Ingress.Valid {
				kubernetes.DeleteIngress(namespace, svc.Ingress.String, env)
			}
			env.Database.DeleteServiceById(r.Context(), svc.ID)
		}

		ids, err := env.Database.GetUnusedVolumeIdentifiers(r.Context(), database.GetUnusedVolumeIdentifiersParams{ProjectID: project.ID, ProjectBranch: branch, Column3: []string{}})
		if err == nil {
			for _, id := range ids {
				kubernetes.DeletePVC(namespace, fmt.Sprintf("pvc-%s", id.String()), env)
			}
		}
		env.Database.DeleteUnusedVolumes(r.Context(), database.DeleteUnusedVolumesParams{ProjectID: project.ID, ProjectBranch: branch, Column3: []string{}})
		kubernetes.DeleteNamespace(namespace, env)
	}

	env.Database.DeleteProject(r.Context(), project.ID)
	w.WriteHeader(http.StatusOK)
}

func GetProjectSecrets(w http.ResponseWriter, r *http.Request) {
	env, ok := r.Context().Value(envKey).(*nimbusEnv.Env)
	if !ok {
		env = nimbusEnv.Null()
	}

	vars := mux.Vars(r)
	projectName := vars["name"]
	apiKey := r.Header.Get(xApiKey)
	user, err := env.Database.GetUserByApiKey(r.Context(), apiKey)
	if err != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	project, err := env.Database.GetProjectByName(r.Context(), projectName)
	if err != nil {
		http.Error(w, "project not found", http.StatusNotFound)
		return
	}

	authorized, err := env.Database.IsUserInProject(r.Context(), database.IsUserInProjectParams{UserID: user.ID, ProjectID: project.ID})
	if err != nil || !authorized {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	showValues := r.URL.Query().Get("values") == "true"
	var resp interface{}
	if showValues {
		vals, err := kubernetes.GetSecretValues(project.Name, env)
		if err != nil {
			env.Logger.ErrorContext(context.Background(), err.Error())
			http.Error(w, "error getting secrets", http.StatusInternalServerError)
			return
		}
		resp = secretsValuesResponse{Secrets: vals}
	} else {
		names, err := kubernetes.ListSecretNames(project.Name, env)
		if err != nil {
			env.Logger.ErrorContext(context.Background(), err.Error())
			http.Error(w, "error getting secrets", http.StatusInternalServerError)
			return
		}
		resp = secretsNamesResponse{Secrets: names}
	}
	json.NewEncoder(w).Encode(resp)
}

func UpdateProjectSecrets(w http.ResponseWriter, r *http.Request) {
	env, ok := r.Context().Value(envKey).(*nimbusEnv.Env)
	if !ok {
		env = nimbusEnv.Null()
	}

	vars := mux.Vars(r)
	projectName := vars["name"]
	apiKey := r.Header.Get(xApiKey)
	user, err := env.Database.GetUserByApiKey(r.Context(), apiKey)
	if err != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	project, err := env.Database.GetProjectByName(r.Context(), projectName)
	if err != nil {
		http.Error(w, "project not found", http.StatusNotFound)
		return
	}

	authorized, err := env.Database.IsUserInProject(r.Context(), database.IsUserInProjectParams{UserID: user.ID, ProjectID: project.ID})
	if err != nil || !authorized {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	var req struct {
		Secrets map[string]string `json:"secrets"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	if req.Secrets == nil {
		req.Secrets = map[string]string{}
	}

	branches, err := env.Database.GetProjectBranches(r.Context(), project.ID)
	if err != nil {
		env.Logger.ErrorContext(context.Background(), err.Error())
		http.Error(w, "error fetching branches", http.StatusInternalServerError)
		return
	}
	if len(branches) == 0 {
		branches = []string{"main"}
	}
	replacer := strings.NewReplacer("/", "-", "_", "-", " ", "-", "#", "", "!", "", "@", "", ".", "")
	for _, branch := range branches {
		ns := project.Name
		if branch != "main" && branch != "master" {
			ns = fmt.Sprintf("%s-%s", project.Name, replacer.Replace(branch))
		}
		if err := kubernetes.UpdateSecret(ns, fmt.Sprintf("%s-env", project.Name), req.Secrets, env); err != nil {
			env.Logger.ErrorContext(context.Background(), err.Error())
			http.Error(w, "error updating secrets", http.StatusInternalServerError)
			return
		}
	}
	w.WriteHeader(http.StatusOK)
}
