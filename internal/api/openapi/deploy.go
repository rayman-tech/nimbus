package openapi

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
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

func (Server) PostDeploy(
	ctx context.Context, request PostDeployRequestObject,
) (PostDeployResponseObject, error) {
	env := env.FromContext(ctx)
	requestID := fmt.Sprintf("%d", requestid.FromContext(ctx))

	slog.DebugContext(ctx, "parsing form")
	const maxSize = 10 << 20 // ~ 10 MB
	form, err := request.Body.ReadForm(maxSize)
	if err != nil {
		slog.ErrorContext(ctx, "failed to read form", "error", err)
		return PostDeploy400JSONResponse{
			Status:  apierror.BadRequest.Status(),
			Code:    apierror.BadRequest.String(),
			Message: "invaild form",
			ErrorId: requestID,
		}, nil
	}

	if env.Config.NimbusStorageClass == "" {
		slog.ErrorContext(ctx, "NimbusStorageClass not defined in config")
		return PostDeploy500JSONResponse{
			Status:  apierror.InternalServerError.Status(),
			Code:    apierror.InternalServerError.String(),
			Message: "Internal Server Error",
			ErrorId: requestID,
		}, nil
	}

	// Read File
	slog.DebugContext(ctx, "retrieving form from file")
	files := form.File["file"]
	if len(files) == 0 {
		slog.ErrorContext(ctx, "no files in form")
		return PostDeploy400JSONResponse{
			Status:  apierror.BadRequest.Status(),
			Code:    apierror.BadRequest.String(),
			Message: "file not found in form",
			ErrorId: requestID,
		}, nil
	}
	fileheader := files[0]
	slog.DebugContext(ctx, "found file", "filename", fileheader.Filename)

	slog.DebugContext(ctx, "reading file content", "filename", fileheader.Filename)
	file, err := fileheader.Open()
	if err != nil {
		slog.ErrorContext(ctx, "failed to open file",
			"filename", fileheader.Filename,
			"error", err)
		return PostDeploy400JSONResponse{
			Status:  apierror.BadRequest.Status(),
			Code:    apierror.BadRequest.String(),
			Message: "invalid file",
			ErrorId: requestID,
		}, nil
	}
	defer func() { _ = file.Close() }()
	content, err := io.ReadAll(file)
	if err != nil {
		slog.ErrorContext(ctx, "failed to read file",
			"filename", fileheader.Filename,
			"error", err)
		return PostDeploy500JSONResponse{
			Status:  apierror.InternalServerError.Status(),
			Code:    apierror.InternalServerError.String(),
			Message: "Internal Server Error",
			ErrorId: requestID,
		}, nil
	}

	slog.DebugContext(ctx, "unmarshaling yaml", "filename", fileheader.Filename)
	var config models.Config
	err = yaml.Unmarshal(content, &config)
	if err != nil {
		slog.ErrorContext(ctx, "failed to unmarshal yaml",
			"filename", fileheader.Filename,
			"error", err)
		return PostDeploy400JSONResponse{
			Status:  apierror.BadRequest.Status(),
			Code:    apierror.BadRequest.String(),
			Message: "failed to parse file - invalid yaml",
			ErrorId: requestID,
		}, nil
	}
	if config.AppName == "" {
		slog.ErrorContext(ctx, "app name is missing in config",
			"filename", fileheader.Filename)
		return PostDeploy400JSONResponse{
			Status:  apierror.BadRequest.Status(),
			Code:    apierror.BadRequest.String(),
			Message: "app name is missing in file",
			ErrorId: requestID,
		}, nil
	}
	if config.AllowBranchPreviews == nil {
		v := true
		config.AllowBranchPreviews = &v
		slog.DebugContext(ctx, "defaulting AllowBranchPreviews to true",
			"app", config.AppName)
	}

	// Retrieve project
	var deployRequest models.DeployRequest
	slog.DebugContext(
		ctx, "retrieving project by name", "name", config.AppName)
	project, err := env.Database.GetProjectByName(ctx, config.AppName)
	if errors.Is(err, pgx.ErrNoRows) {
		slog.ErrorContext(ctx, "project not found",
			"app", config.AppName,
			"error", err)
		return PostDeploy404JSONResponse{
			Status:  apierror.ProjectNotFound.Status(),
			Code:    apierror.ProjectNotFound.String(),
			Message: "project with app name not found",
			ErrorId: requestID,
		}, nil
	} else if err != nil {
		slog.ErrorContext(ctx, "failed to get project",
			"app", config.AppName,
			"error", err)
		return PostDeploy500JSONResponse{
			Status:  apierror.InternalServerError.Status(),
			Code:    apierror.InternalServerError.String(),
			Message: "Internal Server Error",
			ErrorId: requestID,
		}, nil
	}
	deployRequest.ProjectID = project.ID
	slog.DebugContext(ctx, "project retrieved",
		"project", project.Name,
		"project_id", project.ID.String())

	// Check user permissions
	slog.DebugContext(ctx, "checking user project access")
	user := database.UserFromContext(ctx)
	if user == nil {
		return PostDeploy401JSONResponse{
			Status:  apierror.InvalidAPIKey.Status(),
			Code:    apierror.InvalidAPIKey.String(),
			Message: "authentication required",
			ErrorId: requestID,
		}, nil
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
		return PostDeploy500JSONResponse{
			Status:  apierror.InternalServerError.Status(),
			Code:    apierror.InternalServerError.String(),
			Message: "Internal Server Error",
			ErrorId: requestID,
		}, nil
	}
	if !authorized {
		slog.DebugContext(ctx, "user is not authorized to deploy project",
			"project", project.Name,
			"user_id", user.ID.String())
		return PostDeploy403JSONResponse{
			Status:  apierror.InsufficientPermissions.Status(),
			Code:    apierror.InsufficientPermissions.String(),
			Message: "user does not have permissions to deploy project",
			ErrorId: requestID,
		}, nil
	}

	// Read branch
	branches := form.Value["branch"]
	if len(branches) == 0 || branches[0] == "" {
		deployRequest.BranchName = "main"
	} else {
		deployRequest.BranchName = branches[0]
	}
	if config.AllowBranchPreviews != nil &&
		!*config.AllowBranchPreviews &&
		deployRequest.BranchName != "main" && deployRequest.BranchName != "master" {
		return PostDeploy409JSONResponse{
			Status:  apierror.DisabledBranchPreview.Status(),
			Code:    apierror.DisabledBranchPreview.String(),
			Message: "branch previews are disabled",
			ErrorId: requestID,
		}, nil
	}

	// Get services
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
		return PostDeploy500JSONResponse{
			Status:  apierror.InternalServerError.Status(),
			Code:    apierror.InternalServerError.String(),
			Message: "Internal Server Error",
			ErrorId: requestID,
		}, nil
	}
	deployRequest.ExistingServices = servicesList

	// Apply project secrets
	slog.DebugContext(ctx, "applying project secrets",
		"project", project.Name)
	secrets, err := kubernetes.GetSecretValues(ctx, project.Name, env.Config)
	if err != nil {
		slog.ErrorContext(ctx, "failed to get secret values",
			"project", project.Name,
			"error", err)
		return PostDeploy500JSONResponse{
			Status:  apierror.InternalServerError.Status(),
			Code:    apierror.InternalServerError.String(),
			Message: "Internal Server Error",
			ErrorId: requestID,
		}, nil
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
	slog.DebugContext(
		ctx, "validating namespace", "namespace", deployRequest.Namespace)
	created, err := kubernetes.ValidateNamespace(ctx, deployRequest.Namespace, env.Config)
	if err != nil {
		slog.ErrorContext(ctx, "failed to validate namespace",
			"namespace", deployRequest.Namespace,
			"error", err)
		return PostDeploy500JSONResponse{
			Status:  apierror.InternalServerError.Status(),
			Code:    apierror.InternalServerError.String(),
			Message: "Internal Server Error",
			ErrorId: requestID,
		}, nil
	}
	if created && deployRequest.BranchName != "main" && deployRequest.BranchName != "master" {
		mainNS := utils.GetSanitizedNamespace(config.AppName, "main")
		vals, err := kubernetes.GetSecretValues(ctx, mainNS, env.Config)
		if err != nil {
			slog.ErrorContext(ctx, "failed to get secret values",
				"namespace", mainNS,
				"source", "main",
				"error", err)
			return PostDeploy500JSONResponse{
				Status:  apierror.InternalServerError.Status(),
				Code:    apierror.InternalServerError.String(),
				Message: "Internal Server Error",
				ErrorId: requestID,
			}, nil
		}
		if len(vals) > 0 {
			err = kubernetes.UpdateSecret(
				ctx, deployRequest.Namespace, fmt.Sprintf("%s-env", config.AppName),
				vals, env.Config)
			if err != nil {
				slog.ErrorContext(ctx, "failed to update secrets",
					"namespace", deployRequest.Namespace,
					"app", config.AppName,
					"error", err)
				return PostDeploy500JSONResponse{
					Status:  apierror.InternalServerError.Status(),
					Code:    apierror.InternalServerError.String(),
					Message: "Internal Server Error",
					ErrorId: requestID,
				}, nil
			}
		}
	}

	// Delete removed services
	existingServices := make(map[string]*database.Service)
	for _, service := range deployRequest.ExistingServices {
		existingServices[service.ServiceName] = &service
	}
	slog.DebugContext(ctx, "deleting services not present in config",
		"project", project.Name,
		"branch", deployRequest.BranchName)
	serviceNames := make(map[string]bool)
	for _, service := range config.Services {
		if serviceNames[service.Name] {
			slog.ErrorContext(ctx, "duplicate service name", "service", service.Name)
			return PostDeploy422JSONResponse{
				Status:  apierror.UnprocessibleContent.Status(),
				Code:    apierror.UnprocessibleContent.String(),
				Message: "service names must be unique",
				ErrorId: requestID,
			}, nil
		}
		serviceNames[service.Name] = true
	}

	for _, service := range existingServices {
		if _, ok := serviceNames[service.ServiceName]; !ok {
			slog.DebugContext(
				ctx, "deleting deployment",
				"service", service.ServiceName,
				"namespace", deployRequest.Namespace)
			err := kubernetes.DeleteDeployment(
				ctx, deployRequest.Namespace, service.ServiceName, env.Config)
			if err != nil {
				slog.ErrorContext(ctx, "failed to delete deployment",
					"service", service.ServiceName,
					"namespace", deployRequest.Namespace,
					"error", err)
				return PostDeploy500JSONResponse{
					Status:  apierror.InternalServerError.Status(),
					Code:    apierror.InternalServerError.String(),
					Message: "Internal Server Error",
					ErrorId: requestID,
				}, nil
			}

			slog.DebugContext(ctx, "deleting service",
				"service", service.ServiceName,
				"namespace", deployRequest.Namespace)
			err = kubernetes.DeleteService(ctx, deployRequest.Namespace, service.ServiceName, env.Config)
			if err != nil {
				slog.ErrorContext(ctx, "failed to delete service",
					"service", service.ServiceName,
					"namespace", deployRequest.Namespace,
					"error", err)
				return PostDeploy500JSONResponse{
					Status:  apierror.InternalServerError.Status(),
					Code:    apierror.InternalServerError.String(),
					Message: "Internal Server Error",
					ErrorId: requestID,
				}, nil
			}

			if service.Ingress.Valid {
				slog.DebugContext(ctx, "deleting ingress",
					"service", service.ServiceName,
					"ingress", service.Ingress.String)
				err = kubernetes.DeleteIngress(ctx, deployRequest.Namespace, service.Ingress.String, env.Config)
				if err != nil {
					slog.ErrorContext(ctx, "failed to delete ingress",
						"service", service.ServiceName,
						"ingress", service.Ingress.String,
						"error", err)
					return PostDeploy500JSONResponse{
						Status:  apierror.InternalServerError.Status(),
						Code:    apierror.InternalServerError.String(),
						Message: "Internal Server Error",
						ErrorId: requestID,
					}, nil
				}
			}

			slog.DebugContext(ctx, "deleting service in database",
				"service", service.ServiceName)
			err = env.Database.DeleteServiceById(ctx, service.ID)
			if err != nil {
				slog.ErrorContext(ctx, "failed to delete service",
					"service", service.ServiceName,
					"error", err)
				return PostDeploy500JSONResponse{
					Status:  apierror.InternalServerError.Status(),
					Code:    apierror.InternalServerError.String(),
					Message: "Internal Server Error",
					ErrorId: requestID,
				}, nil
			}
		}
	}

	slog.DebugContext(ctx, "creating services and deployments",
		"project", project.Name,
		"branch", deployRequest.BranchName)
	serviceUrls := make(map[string][]string)
	for _, serviceConfig := range config.Services {

		// Create deployment
		slog.DebugContext(ctx, "creating deployment",
			"service", serviceConfig.Name)
		deploymentSpec, err := kubernetes.GenerateDeploymentSpec(
			ctx, &deployRequest, &serviceConfig, env)
		if err != nil {
			slog.ErrorContext(ctx, "failed to create deployment",
				"service", serviceConfig.Name,
				"error", err)
			return PostDeploy500JSONResponse{
				Status:  apierror.InternalServerError.Status(),
				Code:    apierror.InternalServerError.String(),
				Message: "Internal Server Error",
				ErrorId: requestID,
			}, nil
		}
		_, err = kubernetes.CreateDeployment(ctx, deployRequest.Namespace, deploymentSpec, env.Config)
		if err != nil {
			slog.ErrorContext(ctx, "failed to create deployment",
				"service", serviceConfig.Name,
				"error", err)
			return PostDeploy500JSONResponse{
				Status:  apierror.InternalServerError.Status(),
				Code:    apierror.InternalServerError.String(),
				Message: "Internal Server Error",
				ErrorId: requestID,
			}, nil
		}

		// Create service if ports specified or template requires it
		oldService, svcExists := existingServices[serviceConfig.Name]
		var kubeSvc *corev1.Service
		if shouldCreateKubeService(&serviceConfig) {
			slog.DebugContext(ctx, "creating service",
				"service", serviceConfig.Name)
			serviceSpec, err := kubernetes.GenerateServiceSpec(deployRequest.Namespace, &serviceConfig, oldService)
			if err != nil {
				slog.ErrorContext(ctx, "failed to generate service spec",
					"service", serviceConfig.Name,
					"error", err)
				return PostDeploy500JSONResponse{
					Status:  apierror.InternalServerError.Status(),
					Code:    apierror.InternalServerError.String(),
					Message: "Internal Server Error",
					ErrorId: requestID,
				}, nil
			}
			kubeSvc, err = kubernetes.CreateService(ctx, deployRequest.Namespace, serviceSpec, env.Config)
			if err != nil {
				slog.ErrorContext(ctx, "failed to create service",
					"service", serviceConfig.Name,
					"error", err)
				return PostDeploy500JSONResponse{
					Status:  apierror.InternalServerError.Status(),
					Code:    apierror.InternalServerError.String(),
					Message: "Internal Server Error",
					ErrorId: requestID,
				}, nil
			}
		}

		var urls []string
		var newSvc database.Service
		if !svcExists {
			slog.DebugContext(ctx, "creating service in database",
				"service", serviceConfig.Name)
			newSvc, err = env.Database.CreateService(ctx, database.CreateServiceParams{
				ID:            uuid.New(),
				ProjectID:     deployRequest.ProjectID,
				ProjectBranch: deployRequest.BranchName,
				ServiceName:   serviceConfig.Name,
			})
			if err != nil {
				slog.ErrorContext(ctx, "failed to create service in database",
					"service", serviceConfig.Name,
					"error", err)
				return PostDeploy500JSONResponse{
					Status:  apierror.InternalServerError.Status(),
					Code:    apierror.InternalServerError.String(),
					Message: "Internal Server Error",
					ErrorId: requestID,
				}, nil
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

		slog.DebugContext(ctx, "updating service networking in database",
			"service", serviceConfig.Name)
		if kubeSvc == nil {
			if serviceConfig.Template != "http" {
				slog.DebugContext(ctx, "no service ports specified - clearing node ports",
					"service", serviceConfig.Name)
				err := env.Database.SetServiceNodePorts(ctx, database.SetServiceNodePortsParams{
					ID:        serviceID,
					NodePorts: []int32{},
				})
				if err != nil {
					slog.ErrorContext(ctx, "failed to clear service ports",
						"service", serviceConfig.Name,
						"error", err)
					return PostDeploy500JSONResponse{
						Status:  apierror.InternalServerError.Status(),
						Code:    apierror.InternalServerError.String(),
						Message: "Internal Server Error",
						ErrorId: requestID,
					}, nil
				}
			} else {
				if existingIngress != nil {
					slog.DebugContext(ctx, "removing existing ingress",
						"service", serviceConfig.Name,
						"ingress", *existingIngress)
					err = kubernetes.DeleteIngress(ctx, deployRequest.Namespace, *existingIngress, env.Config)
					if err != nil {
						slog.ErrorContext(ctx, "failed to delete ingress",
							"service", serviceConfig.Name,
							"ingress", *existingIngress,
							"error", err)
						return PostDeploy500JSONResponse{
							Status:  apierror.InternalServerError.Status(),
							Code:    apierror.InternalServerError.String(),
							Message: "Internal Server Error",
							ErrorId: requestID,
						}, nil
					}
				}
				err = env.Database.SetServiceIngress(ctx, database.SetServiceIngressParams{
					ID:      serviceID,
					Ingress: pgtype.Text{Valid: false},
				})
				if err != nil {
					slog.ErrorContext(ctx, "failed to set ingress",
						"service", serviceConfig.Name,
						"error", err)
					return PostDeploy500JSONResponse{
						Status:  apierror.InternalServerError.Status(),
						Code:    apierror.InternalServerError.String(),
						Message: "Internal Server Error",
						ErrorId: requestID,
					}, nil
				}
			}
			serviceUrls[serviceConfig.Name] = urls
			continue
		}

		if serviceConfig.Template != "http" {
			if !serviceConfig.Public {
				slog.DebugContext(ctx, "clearing node ports for private service",
					"service", serviceConfig.Name)
				err := env.Database.SetServiceNodePorts(ctx, database.SetServiceNodePortsParams{
					ID:        serviceID,
					NodePorts: []int32{},
				})
				if err != nil {
					slog.ErrorContext(ctx, "failed to update service in database",
						"service", serviceConfig.Name,
						"error", err)
					return PostDeploy500JSONResponse{
						Status:  apierror.InternalServerError.Status(),
						Code:    apierror.InternalServerError.String(),
						Message: "Internal Server Error",
						ErrorId: requestID,
					}, nil
				}
				serviceUrls[serviceConfig.Name] = urls
				continue
			}

			var nodePorts []int32
			slog.DebugContext(ctx, "retrieving node ports from spec",
				"service", serviceConfig.Name)
			for _, port := range kubeSvc.Spec.Ports {
				nodePorts = append(nodePorts, port.NodePort)
				urls = append(urls, utils.FormatServiceURL(env.Config.Domain, port.NodePort))
			}
			err = env.Database.SetServiceNodePorts(ctx, database.SetServiceNodePortsParams{
				ID:        serviceID,
				NodePorts: nodePorts,
			})
			if err != nil {
				slog.ErrorContext(
					ctx, "failed to update service node ports in database", "error", err)
				return PostDeploy500JSONResponse{
					Status:  apierror.InternalServerError.Status(),
					Code:    apierror.InternalServerError.String(),
					Message: "Internal Server Error",
					ErrorId: requestID,
				}, nil
			}
		} else {
			if !serviceConfig.Public {
				if existingIngress != nil {
					slog.DebugContext(ctx, "deleting existing ingress for private service",
						"service", serviceConfig.Name,
						"ingress", *existingIngress)
					err = kubernetes.DeleteIngress(ctx, deployRequest.Namespace, *existingIngress, env.Config)
					if err != nil {
						slog.ErrorContext(ctx, "failed to delete ingress",
							"service", serviceConfig.Name,
							"ingress", *existingIngress,
							"error", err)
						return PostDeploy500JSONResponse{
							Status:  apierror.InternalServerError.Status(),
							Code:    apierror.InternalServerError.String(),
							Message: "Internal Server Error",
							ErrorId: requestID,
						}, nil
					}
				}
				err = env.Database.SetServiceIngress(ctx, database.SetServiceIngressParams{
					ID:      serviceID,
					Ingress: pgtype.Text{Valid: false},
				})
				if err != nil {
					slog.ErrorContext(ctx, "failed to set service ingress",
						"service", serviceConfig.Name,
						"error", err)
					return PostDeploy500JSONResponse{
						Status:  apierror.InternalServerError.Status(),
						Code:    apierror.InternalServerError.String(),
						Message: "Internal Server Error",
						ErrorId: requestID,
					}, nil
				}
				serviceUrls[serviceConfig.Name] = urls
				continue
			}

			slog.DebugContext(ctx, "creating ingress for service",
				"service", serviceConfig.Name)
			ingressSpec, err := kubernetes.GenerateIngressSpec(deployRequest.Namespace, &serviceConfig, existingIngress, deployRequest.BranchName, env.Config)
			if err != nil {
				slog.ErrorContext(ctx, "failed to create ingress for service",
					"service", serviceConfig.Name,
					"error", err)
				return PostDeploy500JSONResponse{
					Status:  apierror.InternalServerError.Status(),
					Code:    apierror.InternalServerError.String(),
					Message: "Internal Server Error",
					ErrorId: requestID,
				}, nil
			}
			newIngress, err := kubernetes.CreateIngress(ctx, deployRequest.Namespace, ingressSpec, env.Config)
			if err != nil {
				slog.ErrorContext(ctx, "failed to create ingress",
					"service", serviceConfig.Name,
					"error", err)
				return PostDeploy500JSONResponse{
					Status:  apierror.InternalServerError.Status(),
					Code:    apierror.InternalServerError.String(),
					Message: "Internal Server Error",
					ErrorId: requestID,
				}, nil
			}
			err = env.Database.SetServiceIngress(ctx, database.SetServiceIngressParams{
				ID:      serviceID,
				Ingress: pgtype.Text{String: newIngress.Spec.Rules[0].Host, Valid: true},
			})
			if err != nil {
				slog.ErrorContext(ctx, "failed to update ingress in database",
					"service", serviceConfig.Name,
					"ingress_host", newIngress.Spec.Rules[0].Host,
					"error", err)
				return PostDeploy500JSONResponse{
					Status:  apierror.InternalServerError.Status(),
					Code:    apierror.InternalServerError.String(),
					Message: "Internal Server Error",
					ErrorId: requestID,
				}, nil
			}
			urls = append(urls, fmt.Sprintf("https://%s", newIngress.Spec.Rules[0].Host))
		}

		slog.DebugContext(ctx, "successfully created service",
			"service", serviceConfig.Name)
		serviceUrls[serviceConfig.Name] = urls
	}

	return PostDeploy200JSONResponse{
		Services: serviceUrls,
	}, nil
}
