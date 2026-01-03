package openapi

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"

	apiError "nimbus/internal/api/error"
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
	requestID := fmt.Sprintf("%d", requestid.FromCtx(ctx))

	env.Logger.DebugContext(ctx, "Parsing form")
	const maxSize = 10 << 20 // ~ 10 MB
	form, err := request.Body.ReadForm(maxSize)
	if err != nil {
		env.Logger.ErrorContext(ctx, "failed to read form", slog.Any("error", err))
		return PostDeploy400JSONResponse{
			Status:  apiError.BadRequest.Status(),
			Code:    apiError.BadRequest.String(),
			Message: "invaild form",
			ErrorId: requestID,
		}, nil
	}

	if env.Config.NimbusStorageClass == "" {
		env.Logger.ErrorContext(ctx, "NimbusStorageClass not defined in config")
		return PostDeploy500JSONResponse{
			Status:  apiError.InternalServerError.Status(),
			Code:    apiError.InternalServerError.String(),
			Message: "Internal Server Error",
			ErrorId: requestID,
		}, nil
	}

	// Read File
	env.Logger.DebugContext(ctx, "retriving form from file")
	files := form.File["file"]
	if len(files) == 0 {
		env.Logger.ErrorContext(ctx, "no files in form")
		return PostDeploy400JSONResponse{
			Status:  apiError.BadRequest.Status(),
			Code:    apiError.BadRequest.String(),
			Message: "file not found in form",
			ErrorId: requestID,
		}, nil
	}
	fileheader := files[0]
	env.Logger.DebugContext(ctx, "found file", slog.String("filename", fileheader.Filename))

	env.Logger.DebugContext(ctx, "reading file content")
	file, err := fileheader.Open()
	if err != nil {
		env.Logger.ErrorContext(ctx, "failed to open file", slog.Any("error", err))
		return PostDeploy400JSONResponse{
			Status:  apiError.BadRequest.Status(),
			Code:    apiError.BadRequest.String(),
			Message: "invalid file",
			ErrorId: requestID,
		}, nil
	}
	defer func() { _ = file.Close() }()
	content, err := io.ReadAll(file)
	if err != nil {
		env.Logger.ErrorContext(ctx, "failed to read file", slog.Any("error", err))
		return PostDeploy500JSONResponse{
			Status:  apiError.InternalServerError.Status(),
			Code:    apiError.InternalServerError.String(),
			Message: "Internal Server Error",
			ErrorId: requestID,
		}, nil
	}

	env.Logger.DebugContext(ctx, "unmarshaling yaml")
	var config models.Config
	err = yaml.Unmarshal(content, &config)
	if err != nil {
		env.Logger.ErrorContext(ctx, "failed to unmarshal yaml", slog.Any("error", err))
		return PostDeploy400JSONResponse{
			Status:  apiError.BadRequest.Status(),
			Code:    apiError.BadRequest.String(),
			Message: "failed to parse file - invalid yaml",
			ErrorId: requestID,
		}, nil
	}
	if config.AppName == "" {
		env.Logger.ErrorContext(ctx, "app name is missing in config")
		return PostDeploy400JSONResponse{
			Status:  apiError.BadRequest.Status(),
			Code:    apiError.BadRequest.String(),
			Message: "app name is missing in file",
			ErrorId: requestID,
		}, nil
	}
	if config.AllowBranchPreviews == nil {
		v := true
		config.AllowBranchPreviews = &v
	}

	// Retrieve project
	var deployRequest models.DeployRequest
	env.Logger.DebugContext(
		ctx, "retrieving project by name", slog.String("name", config.AppName))
	project, err := env.Database.GetProjectByName(ctx, config.AppName)
	if errors.Is(err, pgx.ErrNoRows) {
		env.Logger.ErrorContext(ctx, "project not found", slog.Any("error", err))
		return PostDeploy404JSONResponse{
			Status:  apiError.ProjectNotFound.Status(),
			Code:    apiError.ProjectNotFound.String(),
			Message: "project with app name not found",
			ErrorId: requestID,
		}, nil
	} else if err != nil {
		env.Logger.ErrorContext(ctx, "failed to get project", slog.Any("error", err))
		return PostDeploy500JSONResponse{
			Status:  apiError.InternalServerError.Status(),
			Code:    apiError.InternalServerError.String(),
			Message: "Internal Server Error",
			ErrorId: requestID,
		}, nil
	}
	deployRequest.ProjectID = project.ID

	// Check user permissions
	env.Logger.DebugContext(ctx, "checking user project access")
	user := database.UserFromContext(ctx)
	authorized, err := env.Database.IsUserInProject(ctx, database.IsUserInProjectParams{
		UserID:    user.ID,
		ProjectID: project.ID,
	})
	if err != nil {
		env.Logger.ErrorContext(ctx, "failed to check user access", slog.Any("error", err))
		return PostDeploy500JSONResponse{
			Status:  apiError.InternalServerError.Status(),
			Code:    apiError.InternalServerError.String(),
			Message: "Internal Server Error",
			ErrorId: requestID,
		}, nil
	}
	if !authorized {
		env.Logger.DebugContext(ctx, "user is not authorized to deploy project")
		return PostDeploy403JSONResponse{
			Status:  apiError.InsufficientPermissions.Status(),
			Code:    apiError.InsufficientPermissions.String(),
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
			Status:  apiError.DisabledBranchPreview.Status(),
			Code:    apiError.DisabledBranchPreview.String(),
			Message: "branch previews are disabled",
			ErrorId: requestID,
		}, nil
	}

	// Get services
	env.Logger.DebugContext(ctx, "getting project services")
	servicesList, err := env.Database.GetServicesByProject(ctx, database.GetServicesByProjectParams{
		ProjectID:     deployRequest.ProjectID,
		ProjectBranch: deployRequest.BranchName,
	})
	if err != nil {
		env.Logger.ErrorContext(ctx, "failed to get project services", slog.Any("error", err))
		return PostDeploy500JSONResponse{
			Status:  apiError.InternalServerError.Status(),
			Code:    apiError.InternalServerError.String(),
			Message: "Internal Server Error",
			ErrorId: requestID,
		}, nil
	}
	deployRequest.ExistingServices = servicesList

	// Apply project secrets
	env.Logger.DebugContext(ctx, "applying project secrets")
	secrets, err := kubernetes.GetSecretValues(project.Name, env)
	if err != nil {
		env.Logger.ErrorContext(ctx, "failed to get secret values", slog.Any("error", err))
		return PostDeploy500JSONResponse{
			Status:  apiError.InternalServerError.Status(),
			Code:    apiError.InternalServerError.String(),
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
	env.Logger.DebugContext(
		ctx, "Validating namespace", slog.String("namespace", deployRequest.Namespace))
	created, err := kubernetes.ValidateNamespace(ctx, deployRequest.Namespace, env)
	if err != nil {
		env.Logger.ErrorContext(ctx, "failed to validate namespace", slog.Any("error", err))
		return PostDeploy500JSONResponse{
			Status:  apiError.InternalServerError.Status(),
			Code:    apiError.InternalServerError.String(),
			Message: "Internal Server Error",
			ErrorId: requestID,
		}, nil
	}
	if created && deployRequest.BranchName != "main" && deployRequest.BranchName != "master" {
		mainNS := utils.GetSanitizedNamespace(config.AppName, "main")
		vals, err := kubernetes.GetSecretValues(mainNS, env)
		if err != nil {
			env.Logger.ErrorContext(ctx, "failed to get secret values", slog.Any("error", err))
			return PostDeploy500JSONResponse{
				Status:  apiError.InternalServerError.Status(),
				Code:    apiError.InternalServerError.String(),
				Message: "Internal Server Error",
				ErrorId: requestID,
			}, nil
		}
		if len(vals) > 0 {
			err = kubernetes.UpdateSecret(
				ctx, deployRequest.Namespace, fmt.Sprintf("%s-env", config.AppName),
				vals, env)
			if err != nil {
				env.Logger.ErrorContext(ctx, "failed to update secrets", slog.Any("error", err))
				return PostDeploy500JSONResponse{
					Status:  apiError.InternalServerError.Status(),
					Code:    apiError.InternalServerError.String(),
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
	env.Logger.DebugContext(ctx, "deleting services not present in config")
	serviceNames := make(map[string]bool)
	for _, service := range config.Services {
		if serviceNames[service.Name] {
			env.Logger.ErrorContext(ctx, "duplicate service name", slog.String("service", service.Name))
			return PostDeploy422JSONResponse{
				Status:  apiError.UnprocessibleContent.Status(),
				Code:    apiError.UnprocessibleContent.String(),
				Message: "service names must be unique",
				ErrorId: requestID,
			}, nil
		}
		serviceNames[service.Name] = true
	}

	for _, service := range existingServices {
		if _, ok := serviceNames[service.ServiceName]; !ok {
			env.Logger.DebugContext(
				ctx, "deleting deployment", slog.String("service", service.ServiceName))
			err := kubernetes.DeleteDeployment(
				ctx, deployRequest.Namespace, service.ServiceName, env)
			if err != nil {
				env.Logger.ErrorContext(ctx, "failed to delete deployment", slog.Any("error", err))
				return PostDeploy500JSONResponse{
					Status:  apiError.InternalServerError.Status(),
					Code:    apiError.InternalServerError.String(),
					Message: "Internal Server Error",
					ErrorId: requestID,
				}, nil
			}

			env.Logger.DebugContext(ctx, "deleting service")
			err = kubernetes.DeleteService(ctx, deployRequest.Namespace, service.ServiceName, env)
			if err != nil {
				env.Logger.ErrorContext(ctx, "failed to delete service", slog.Any("error", err))
				return PostDeploy500JSONResponse{
					Status:  apiError.InternalServerError.Status(),
					Code:    apiError.InternalServerError.String(),
					Message: "Internal Server Error",
					ErrorId: requestID,
				}, nil
			}

			if service.Ingress.Valid {
				env.Logger.DebugContext(ctx, "deleting ingress")
				err = kubernetes.DeleteIngress(ctx, deployRequest.Namespace, service.Ingress.String, env)
				if err != nil {
					env.Logger.ErrorContext(ctx, "failed to delete ingress", slog.Any("error", err))
					return PostDeploy500JSONResponse{
						Status:  apiError.InternalServerError.Status(),
						Code:    apiError.InternalServerError.String(),
						Message: "Internal Server Error",
						ErrorId: requestID,
					}, nil
				}
			}

			env.Logger.DebugContext(ctx, "deleting service in databasse")
			err = env.Database.DeleteServiceById(ctx, service.ID)
			if err != nil {
				env.Logger.ErrorContext(ctx, "failed to delete service", slog.Any("error", err))
				return PostDeploy500JSONResponse{
					Status:  apiError.InternalServerError.Status(),
					Code:    apiError.InternalServerError.String(),
					Message: "Internal Server Error",
					ErrorId: requestID,
				}, nil
			}
		}
	}

	env.Logger.DebugContext(ctx, "Creating services and deployments")
	serviceUrls := make(map[string][]string)
	for _, serviceConfig := range config.Services {

		// Create deployment
		env.Logger.DebugContext(ctx, "creating deployment")
		deploymentSpec, err := kubernetes.GenerateDeploymentSpec(
			ctx, &deployRequest, &serviceConfig, env)
		if err != nil {
			env.Logger.ErrorContext(ctx, "failed to create deployment", slog.Any("error", err))
			return PostDeploy500JSONResponse{
				Status:  apiError.InternalServerError.Status(),
				Code:    apiError.InternalServerError.String(),
				Message: "Internal Server Error",
				ErrorId: requestID,
			}, nil
		}
		_, err = kubernetes.CreateDeployment(ctx, deployRequest.Namespace, deploymentSpec, env)
		if err != nil {
			env.Logger.ErrorContext(ctx, "failed to create deployment", slog.Any("error", err))
			return PostDeploy500JSONResponse{
				Status:  apiError.InternalServerError.Status(),
				Code:    apiError.InternalServerError.String(),
				Message: "Internal Server Error",
				ErrorId: requestID,
			}, nil
		}

		// Create service if ports specified or template requires it
		oldService, svcExists := existingServices[serviceConfig.Name]
		var kubeSvc *corev1.Service
		if shouldCreateKubeService(&serviceConfig) {
			env.Logger.DebugContext(ctx, "creating service", slog.String("service", serviceConfig.Name))
			serviceSpec, err := kubernetes.GenerateServiceSpec(deployRequest.Namespace, &serviceConfig, oldService)
			if err != nil {
				env.Logger.ErrorContext(ctx, "failed to generate service spec", slog.Any("error", err))
				return PostDeploy500JSONResponse{
					Status:  apiError.InternalServerError.Status(),
					Code:    apiError.InternalServerError.String(),
					Message: "Internal Server Error",
					ErrorId: requestID,
				}, nil
			}
			kubeSvc, err = kubernetes.CreateService(ctx, deployRequest.Namespace, serviceSpec, env)
			if err != nil {
				env.Logger.ErrorContext(ctx, "failed to create service", slog.Any("error", err))
				return PostDeploy500JSONResponse{
					Status:  apiError.InternalServerError.Status(),
					Code:    apiError.InternalServerError.String(),
					Message: "Internal Server Error",
					ErrorId: requestID,
				}, nil
			}
		}

		var urls []string
		var newSvc database.Service
		if !svcExists {
			env.Logger.DebugContext(ctx, "Creating service in database")
			newSvc, err = env.Database.CreateService(ctx, database.CreateServiceParams{
				ID:            uuid.New(),
				ProjectID:     deployRequest.ProjectID,
				ProjectBranch: deployRequest.BranchName,
				ServiceName:   serviceConfig.Name,
			})
			if err != nil {
				env.Logger.ErrorContext(ctx, "failed to create service in database", slog.Any("error", err))
				return PostDeploy500JSONResponse{
					Status:  apiError.InternalServerError.Status(),
					Code:    apiError.InternalServerError.String(),
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

		env.Logger.DebugContext(ctx, "updating service networking in database")
		if kubeSvc == nil {
			if serviceConfig.Template != "http" {
				env.Logger.DebugContext(ctx, "no service ports specified - clearing node ports")
				err := env.Database.SetServiceNodePorts(ctx, database.SetServiceNodePortsParams{
					ID:        serviceID,
					NodePorts: []int32{},
				})
				if err != nil {
					env.Logger.ErrorContext(ctx, "failed to clear service ports", slog.Any("error", err))
					return PostDeploy500JSONResponse{
						Status:  apiError.InternalServerError.Status(),
						Code:    apiError.InternalServerError.String(),
						Message: "Internal Server Error",
						ErrorId: requestID,
					}, nil
				}
			} else {
				if existingIngress != nil {
					env.Logger.DebugContext(ctx, "removing existing ingress")
					err = kubernetes.DeleteIngress(ctx, deployRequest.Namespace, *existingIngress, env)
					if err != nil {
						env.Logger.ErrorContext(ctx, "failed to delete ingress", slog.Any("error", err))
						return PostDeploy500JSONResponse{
							Status:  apiError.InternalServerError.Status(),
							Code:    apiError.InternalServerError.String(),
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
					env.Logger.ErrorContext(ctx, "failed to set ingress", slog.Any("error", err))
					return PostDeploy500JSONResponse{
						Status:  apiError.InternalServerError.Status(),
						Code:    apiError.InternalServerError.String(),
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
				env.Logger.DebugContext(ctx, "clearing node ports for private service")
				err := env.Database.SetServiceNodePorts(ctx, database.SetServiceNodePortsParams{
					ID:        serviceID,
					NodePorts: []int32{},
				})
				if err != nil {
					env.Logger.ErrorContext(ctx, "failed to update service in database", slog.Any("error", err))
					return PostDeploy500JSONResponse{
						Status:  apiError.InternalServerError.Status(),
						Code:    apiError.InternalServerError.String(),
						Message: "Internal Server Error",
						ErrorId: requestID,
					}, nil
				}
				serviceUrls[serviceConfig.Name] = urls
				continue
			}

			var nodePorts []int32
			env.Logger.DebugContext(ctx, "retrieving node ports from spec")
			for _, port := range kubeSvc.Spec.Ports {
				nodePorts = append(nodePorts, port.NodePort)
				urls = append(urls, utils.FormatServiceURL(env.Config.Domain, port.NodePort))
			}
			err = env.Database.SetServiceNodePorts(ctx, database.SetServiceNodePortsParams{
				ID:        serviceID,
				NodePorts: nodePorts,
			})
			if err != nil {
				env.Logger.ErrorContext(
					ctx, "failed to update service node ports in database", slog.Any("error", err))
				return PostDeploy500JSONResponse{
					Status:  apiError.InternalServerError.Status(),
					Code:    apiError.InternalServerError.String(),
					Message: "Internal Server Error",
					ErrorId: requestID,
				}, nil
			}
		} else {
			if !serviceConfig.Public {
				if existingIngress != nil {
					env.Logger.DebugContext(ctx, "deleting existing ingress for private service")
					err = kubernetes.DeleteIngress(ctx, deployRequest.Namespace, *existingIngress, env)
					if err != nil {
						env.Logger.ErrorContext(ctx, "failed to delete ingress", slog.Any("error", err))
						return PostDeploy500JSONResponse{
							Status:  apiError.InternalServerError.Status(),
							Code:    apiError.InternalServerError.String(),
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
					env.Logger.ErrorContext(ctx, "failed to set service ingress", slog.Any("error", err))
					return PostDeploy500JSONResponse{
						Status:  apiError.InternalServerError.Status(),
						Code:    apiError.InternalServerError.String(),
						Message: "Internal Server Error",
						ErrorId: requestID,
					}, nil
				}
				serviceUrls[serviceConfig.Name] = urls
				continue
			}

			env.Logger.DebugContext(ctx, "creating ingress for service")
			ingressSpec, err := kubernetes.GenerateIngressSpec(deployRequest.Namespace, &serviceConfig, existingIngress, env)
			if err != nil {
				env.Logger.ErrorContext(ctx, "failed to create ingress for service", slog.Any("error", err))
				return PostDeploy500JSONResponse{
					Status:  apiError.InternalServerError.Status(),
					Code:    apiError.InternalServerError.String(),
					Message: "Internal Server Error",
					ErrorId: requestID,
				}, nil
			}
			newIngress, err := kubernetes.CreateIngress(ctx, deployRequest.Namespace, ingressSpec, env)
			if err != nil {
				env.Logger.ErrorContext(ctx, "failed to create ingress", slog.Any("error", err))
				return PostDeploy500JSONResponse{
					Status:  apiError.InternalServerError.Status(),
					Code:    apiError.InternalServerError.String(),
					Message: "Internal Server Error",
					ErrorId: requestID,
				}, nil
			}
			err = env.Database.SetServiceIngress(ctx, database.SetServiceIngressParams{
				ID:      serviceID,
				Ingress: pgtype.Text{String: newIngress.Spec.Rules[0].Host, Valid: true},
			})
			if err != nil {
				env.Logger.ErrorContext(ctx, "failed to update ingress in database", slog.Any("error", err))
				return PostDeploy500JSONResponse{
					Status:  apiError.InternalServerError.Status(),
					Code:    apiError.InternalServerError.String(),
					Message: "Internal Server Error",
					ErrorId: requestID,
				}, nil
			}
			urls = append(urls, fmt.Sprintf("https://%s", newIngress.Spec.Rules[0].Host))
		}

		env.Logger.DebugContext(ctx, "Successfully created service in database")
		serviceUrls[serviceConfig.Name] = urls
	}

	return PostDeploy200JSONResponse{
		Services: serviceUrls,
	}, nil
}
