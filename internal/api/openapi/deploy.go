package openapi

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"strings"

	apierror "nimbus/internal/api/error"
	"nimbus/internal/api/requestid"
	"nimbus/internal/database"
	"nimbus/internal/env"
	"nimbus/internal/kubernetes"
	"nimbus/internal/models"
	"nimbus/internal/utils"

	"github.com/goccy/go-yaml"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	corev1 "k8s.io/api/core/v1"
)

func shouldCreateKubeService(service *models.Service) bool {
	if len(service.Network.Ports) > 0 {
		return true
	}
	switch service.Template {
	case "postgres", "redis", "http":
		return true
	default:
		return false
	}
}

// parseDeployForm reads the multipart form, parses the YAML config, validates
// the project/user, and returns the parsed config, deploy request, and request
// ID. On failure it returns the appropriate response object.
func parseDeployForm(
	ctx context.Context, form *multipart.Form, env *env.Env, rid string,
) (*models.Config, *models.DeployRequest, PostDeployResponseObject) {
	if env.Config.NimbusStorageClass == "" {
		slog.ErrorContext(ctx, "NimbusStorageClass not defined in config")
		return nil, nil, PostDeploy500JSONResponse(internalError(rid))
	}

	// Read File
	slog.DebugContext(ctx, "retrieving form from file")
	files := form.File["file"]
	if len(files) == 0 {
		slog.ErrorContext(ctx, "no files in form")
		return nil, nil, PostDeploy400JSONResponse{
			Status:  apierror.BadRequest.Status(),
			Code:    apierror.BadRequest.String(),
			Message: "file not found in form",
			ErrorId: rid,
		}
	}
	fileheader := files[0]
	slog.DebugContext(ctx, "found file", "filename", fileheader.Filename)

	slog.DebugContext(ctx, "reading file content", "filename", fileheader.Filename)
	file, err := fileheader.Open()
	if err != nil {
		slog.ErrorContext(ctx, "failed to open file",
			"filename", fileheader.Filename,
			"error", err)
		return nil, nil, PostDeploy400JSONResponse{
			Status:  apierror.BadRequest.Status(),
			Code:    apierror.BadRequest.String(),
			Message: "invalid file",
			ErrorId: rid,
		}
	}
	defer func() { _ = file.Close() }()
	content, err := io.ReadAll(file)
	if err != nil {
		slog.ErrorContext(ctx, "failed to read file",
			"filename", fileheader.Filename,
			"error", err)
		return nil, nil, PostDeploy500JSONResponse(internalError(rid))
	}

	slog.DebugContext(ctx, "unmarshaling yaml", "filename", fileheader.Filename)
	var config models.Config
	err = yaml.Unmarshal(content, &config)
	if err != nil {
		slog.ErrorContext(ctx, "failed to unmarshal yaml",
			"filename", fileheader.Filename,
			"error", err)
		return nil, nil, PostDeploy400JSONResponse{
			Status:  apierror.BadRequest.Status(),
			Code:    apierror.BadRequest.String(),
			Message: "failed to parse file - invalid yaml",
			ErrorId: rid,
		}
	}
	if config.AppName == "" {
		slog.ErrorContext(ctx, "app name is missing in config",
			"filename", fileheader.Filename)
		return nil, nil, PostDeploy400JSONResponse{
			Status:  apierror.BadRequest.Status(),
			Code:    apierror.BadRequest.String(),
			Message: "app name is missing in file",
			ErrorId: rid,
		}
	}
	if config.AllowBranchPreviews == nil {
		v := true
		config.AllowBranchPreviews = &v
		slog.DebugContext(ctx, "defaulting AllowBranchPreviews to true",
			"app", config.AppName)
	}

	// Retrieve project
	var deployRequest models.DeployRequest
	slog.DebugContext(ctx, "retrieving project by name", "name", config.AppName)
	project, err := env.Database.GetProjectByName(ctx, config.AppName)
	if errors.Is(err, pgx.ErrNoRows) {
		slog.ErrorContext(ctx, "project not found",
			"app", config.AppName,
			"error", err)
		return nil, nil, PostDeploy404JSONResponse{
			Status:  apierror.ProjectNotFound.Status(),
			Code:    apierror.ProjectNotFound.String(),
			Message: "project with app name not found",
			ErrorId: rid,
		}
	} else if err != nil {
		slog.ErrorContext(ctx, "failed to get project",
			"app", config.AppName,
			"error", err)
		return nil, nil, PostDeploy500JSONResponse(internalError(rid))
	}
	deployRequest.ProjectID = project.ID
	slog.DebugContext(ctx, "project retrieved",
		"project", project.Name,
		"project_id", project.ID.String())

	// Check user permissions
	slog.DebugContext(ctx, "checking user project access")
	user := database.UserFromContext(ctx)
	if user == nil {
		return nil, nil, PostDeploy401JSONResponse(authError(rid))
	}
	authorized, err := env.Database.IsUserInProject(ctx, database.IsUserInProjectParams{
		UserID:    user.ID,
		ProjectID: project.ID,
	})
	if err != nil {
		slog.ErrorContext(ctx, "failed to check user access",
			"project", project.Name,
			"project_id", project.ID.String(),
			"error", err)
		return nil, nil, PostDeploy500JSONResponse(internalError(rid))
	}
	if !authorized {
		slog.DebugContext(ctx, "user is not authorized to deploy project",
			"project", project.Name,
			"user_id", user.ID.String())
		return nil, nil, PostDeploy403JSONResponse(forbiddenError(rid, "user does not have permissions to deploy project"))
	}

	// Read branch
	branches := form.Value["branch"]
	if len(branches) == 0 || branches[0] == "" {
		deployRequest.BranchName = utils.DefaultBranch
	} else {
		deployRequest.BranchName = branches[0]
	}
	if config.AllowBranchPreviews != nil &&
		!*config.AllowBranchPreviews &&
		!utils.IsMainBranch(deployRequest.BranchName) {
		return nil, nil, PostDeploy409JSONResponse{
			Status:  apierror.DisabledBranchPreview.Status(),
			Code:    apierror.DisabledBranchPreview.String(),
			Message: "branch previews are disabled",
			ErrorId: rid,
		}
	}

	// Get existing services
	slog.DebugContext(ctx, "getting project services",
		"project", project.Name,
		"branch", deployRequest.BranchName)
	servicesList, err := env.Database.GetServicesByProject(ctx, database.GetServicesByProjectParams{
		ProjectID:     deployRequest.ProjectID,
		ProjectBranch: deployRequest.BranchName,
	})
	if err != nil {
		slog.ErrorContext(ctx, "failed to get project services",
			"project", project.Name,
			"branch", deployRequest.BranchName,
			"error", err)
		return nil, nil, PostDeploy500JSONResponse(internalError(rid))
	}
	deployRequest.ExistingServices = servicesList

	// Apply project secrets
	slog.DebugContext(ctx, "applying project secrets",
		"project", project.Name)
	secrets, err := kubernetes.GetSecretValues(ctx, project.Name)
	if err != nil {
		slog.ErrorContext(ctx, "failed to get secret values",
			"project", project.Name,
			"error", err)
		return nil, nil, PostDeploy500JSONResponse(internalError(rid))
	}
	for i, service := range config.Services {
		for j, variable := range service.Env {
			key, prefFound := strings.CutPrefix(variable.Value, "${")
			key, suffFound := strings.CutSuffix(key, "}")
			if !prefFound || !suffFound {
				continue
			}
			if secretVal, ok := secrets[key]; ok {
				config.Services[i].Env[j].Value = secretVal
			}
		}
	}

	// Validate namespace
	deployRequest.Namespace = utils.GetSanitizedNamespace(
		project.Name, deployRequest.BranchName)
	slog.DebugContext(ctx, "validating namespace", "namespace", deployRequest.Namespace)
	created, err := kubernetes.ValidateNamespace(ctx, deployRequest.Namespace)
	if err != nil {
		slog.ErrorContext(ctx, "failed to validate namespace",
			"namespace", deployRequest.Namespace,
			"error", err)
		return nil, nil, PostDeploy500JSONResponse(internalError(rid))
	}
	if created && !utils.IsMainBranch(deployRequest.BranchName) {
		mainNS := utils.GetSanitizedNamespace(config.AppName, utils.DefaultBranch)
		vals, err := kubernetes.GetSecretValues(ctx, mainNS)
		if err != nil {
			slog.ErrorContext(ctx, "failed to get secret values",
				"namespace", mainNS,
				"source", "main",
				"error", err)
			return nil, nil, PostDeploy500JSONResponse(internalError(rid))
		}
		if len(vals) > 0 {
			err = kubernetes.UpdateSecret(
				ctx, deployRequest.Namespace, fmt.Sprintf("%s-env", config.AppName),
				vals)
			if err != nil {
				slog.ErrorContext(ctx, "failed to update secrets",
					"namespace", deployRequest.Namespace,
					"app", config.AppName,
					"error", err)
				return nil, nil, PostDeploy500JSONResponse(internalError(rid))
			}
		}
	}

	return &config, &deployRequest, nil
}

// deployService deploys a single service: creates the deployment, k8s service,
// ingress, and DB records. Returns the URLs assigned to this service.
func deployService(
	ctx context.Context, deployRequest *models.DeployRequest,
	serviceConfig *models.Service, existingService *database.Service,
	env *env.Env,
) ([]string, error) {
	// Create deployment
	slog.DebugContext(ctx, "creating deployment", "service", serviceConfig.Name)
	deploymentSpec, err := kubernetes.GenerateDeploymentSpec(ctx, deployRequest, serviceConfig, env)
	if err != nil {
		return nil, fmt.Errorf("generating deployment spec for %s: %w", serviceConfig.Name, err)
	}
	_, err = kubernetes.CreateDeployment(ctx, deployRequest.Namespace, deploymentSpec)
	if err != nil {
		return nil, fmt.Errorf("creating deployment for %s: %w", serviceConfig.Name, err)
	}

	// Create k8s service if needed
	var kubeSvc *corev1.Service
	if shouldCreateKubeService(serviceConfig) {
		slog.DebugContext(ctx, "creating service", "service", serviceConfig.Name)
		serviceSpec, err := kubernetes.GenerateServiceSpec(deployRequest.Namespace, serviceConfig, existingService)
		if err != nil {
			return nil, fmt.Errorf("generating service spec for %s: %w", serviceConfig.Name, err)
		}
		kubeSvc, err = kubernetes.CreateService(ctx, deployRequest.Namespace, serviceSpec)
		if err != nil {
			return nil, fmt.Errorf("creating service for %s: %w", serviceConfig.Name, err)
		}
	}

	// Ensure DB record exists
	svcExists := existingService != nil
	var newSvc database.Service
	if !svcExists {
		slog.DebugContext(ctx, "creating service in database", "service", serviceConfig.Name)
		newSvc, err = env.Database.CreateService(ctx, database.CreateServiceParams{
			ID:            uuid.New(),
			ProjectID:     deployRequest.ProjectID,
			ProjectBranch: deployRequest.BranchName,
			ServiceName:   serviceConfig.Name,
		})
		if err != nil {
			return nil, fmt.Errorf("creating service in database for %s: %w", serviceConfig.Name, err)
		}
	}

	var serviceID uuid.UUID
	var existingIngress *string
	if svcExists {
		serviceID = existingService.ID
		if existingService.Ingress.Valid {
			existingIngress = &existingService.Ingress.String
		}
	} else {
		serviceID = newSvc.ID
	}

	// Update networking
	var urls []string
	slog.DebugContext(ctx, "updating service networking in database", "service", serviceConfig.Name)
	if kubeSvc == nil {
		if serviceConfig.Template != "http" {
			slog.DebugContext(ctx, "no service ports specified - clearing node ports",
				"service", serviceConfig.Name)
			if err := env.Database.SetServiceNodePorts(ctx, database.SetServiceNodePortsParams{
				ID:        serviceID,
				NodePorts: []int32{},
			}); err != nil {
				return nil, fmt.Errorf("clearing service ports for %s: %w", serviceConfig.Name, err)
			}
		} else {
			if existingIngress != nil {
				slog.DebugContext(ctx, "removing existing ingress",
					"service", serviceConfig.Name,
					"ingress", *existingIngress)
				if err := kubernetes.DeleteIngress(ctx, deployRequest.Namespace, *existingIngress); err != nil {
					slog.ErrorContext(ctx, "failed to delete ingress",
						"service", serviceConfig.Name,
						"ingress", *existingIngress,
						"error", err)
				}
			}
			if err := env.Database.SetServiceIngress(ctx, database.SetServiceIngressParams{
				ID:      serviceID,
				Ingress: pgtype.Text{Valid: false},
			}); err != nil {
				return nil, fmt.Errorf("setting ingress for %s: %w", serviceConfig.Name, err)
			}
		}
		return urls, nil
	}

	if serviceConfig.Template != "http" {
		if !serviceConfig.Public {
			slog.DebugContext(ctx, "clearing node ports for private service",
				"service", serviceConfig.Name)
			if err := env.Database.SetServiceNodePorts(ctx, database.SetServiceNodePortsParams{
				ID:        serviceID,
				NodePorts: []int32{},
			}); err != nil {
				return nil, fmt.Errorf("clearing node ports for %s: %w", serviceConfig.Name, err)
			}
			return urls, nil
		}

		var nodePorts []int32
		slog.DebugContext(ctx, "retrieving node ports from spec",
			"service", serviceConfig.Name)
		for _, port := range kubeSvc.Spec.Ports {
			nodePorts = append(nodePorts, port.NodePort)
			urls = append(urls, utils.FormatServiceURL(env.Config.Domain, port.NodePort))
		}
		if err := env.Database.SetServiceNodePorts(ctx, database.SetServiceNodePortsParams{
			ID:        serviceID,
			NodePorts: nodePorts,
		}); err != nil {
			return nil, fmt.Errorf("updating node ports for %s: %w", serviceConfig.Name, err)
		}
	} else {
		if !serviceConfig.Public {
			if existingIngress != nil {
				slog.DebugContext(ctx, "deleting existing ingress for private service",
					"service", serviceConfig.Name,
					"ingress", *existingIngress)
				if err := kubernetes.DeleteIngress(ctx, deployRequest.Namespace, *existingIngress); err != nil {
					slog.ErrorContext(ctx, "failed to delete ingress",
						"service", serviceConfig.Name,
						"ingress", *existingIngress,
						"error", err)
				}
			}
			if err := env.Database.SetServiceIngress(ctx, database.SetServiceIngressParams{
				ID:      serviceID,
				Ingress: pgtype.Text{Valid: false},
			}); err != nil {
				return nil, fmt.Errorf("setting ingress for %s: %w", serviceConfig.Name, err)
			}
			return urls, nil
		}

		slog.DebugContext(ctx, "creating ingress for service",
			"service", serviceConfig.Name)
		ingressSpec, err := kubernetes.GenerateIngressSpec(deployRequest.Namespace, serviceConfig, existingIngress, deployRequest.BranchName, env.Config)
		if err != nil {
			return nil, fmt.Errorf("generating ingress spec for %s: %w", serviceConfig.Name, err)
		}
		newIngress, err := kubernetes.CreateIngress(ctx, deployRequest.Namespace, ingressSpec)
		if err != nil {
			return nil, fmt.Errorf("creating ingress for %s: %w", serviceConfig.Name, err)
		}
		if err := env.Database.SetServiceIngress(ctx, database.SetServiceIngressParams{
			ID:      serviceID,
			Ingress: pgtype.Text{String: newIngress.Spec.Rules[0].Host, Valid: true},
		}); err != nil {
			return nil, fmt.Errorf("updating ingress in database for %s: %w", serviceConfig.Name, err)
		}
		urls = append(urls, fmt.Sprintf("https://%s", newIngress.Spec.Rules[0].Host))
	}

	slog.DebugContext(ctx, "successfully created service", "service", serviceConfig.Name)
	return urls, nil
}

func (Server) PostDeploy(
	ctx context.Context, request PostDeployRequestObject,
) (PostDeployResponseObject, error) {
	env := env.FromContext(ctx)
	rid := fmt.Sprintf("%d", requestid.FromContext(ctx))

	slog.DebugContext(ctx, "parsing form")
	const maxSize = 10 << 20 // ~ 10 MB
	form, err := request.Body.ReadForm(maxSize)
	if err != nil {
		slog.ErrorContext(ctx, "failed to read form", "error", err)
		return PostDeploy400JSONResponse{
			Status:  apierror.BadRequest.Status(),
			Code:    apierror.BadRequest.String(),
			Message: "invaild form",
			ErrorId: rid,
		}, nil
	}

	config, deployRequest, errResp := parseDeployForm(ctx, form, env, rid)
	if errResp != nil {
		return errResp, nil
	}

	// Build existing services map and validate unique names
	existingServices := make(map[string]*database.Service)
	for _, service := range deployRequest.ExistingServices {
		existingServices[service.ServiceName] = &service
	}

	slog.DebugContext(ctx, "validating service names")
	serviceNames := make(map[string]bool)
	for _, service := range config.Services {
		if serviceNames[service.Name] {
			slog.ErrorContext(ctx, "duplicate service name", "service", service.Name)
			return PostDeploy422JSONResponse{
				Status:  apierror.UnprocessibleContent.Status(),
				Code:    apierror.UnprocessibleContent.String(),
				Message: "service names must be unique",
				ErrorId: rid,
			}, nil
		}
		serviceNames[service.Name] = true
	}

	// Delete stale services (k8s errors logged, not fatal)
	if err := deleteStaleServices(ctx, deployRequest.Namespace, existingServices, serviceNames, env.Database); err != nil {
		slog.ErrorContext(ctx, "failed to delete stale services", "error", err)
		return PostDeploy500JSONResponse(internalError(rid)), nil
	}

	// Deploy each service
	slog.DebugContext(ctx, "creating services and deployments",
		"project_id", deployRequest.ProjectID.String(),
		"branch", deployRequest.BranchName)
	serviceUrls := make(map[string][]string)
	for _, serviceConfig := range config.Services {
		oldService := existingServices[serviceConfig.Name]
		urls, err := deployService(ctx, deployRequest, &serviceConfig, oldService, env)
		if err != nil {
			slog.ErrorContext(ctx, "failed to deploy service",
				"service", serviceConfig.Name,
				"error", err)
			return PostDeploy500JSONResponse(internalError(rid)), nil
		}
		serviceUrls[serviceConfig.Name] = urls
	}

	return PostDeploy200JSONResponse{
		Services: serviceUrls,
	}, nil
}
