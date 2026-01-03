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

	env.Logger.DebugContext(ctx, "parsing form")
	const maxSize = 10 << 20 // ~ 10 MB
	form, err := request.Body.ReadForm(maxSize)
	if err != nil {
		env.Logger.ErrorContext(ctx, "failed to read form", slog.Any("error", err))
		return PostDeploy400JSONResponse{
			Status:  apierror.BadRequest.Status(),
			Code:    apierror.BadRequest.String(),
			Message: "invaild form",
			ErrorId: requestID,
		}, nil
	}

	if env.Config.NimbusStorageClass == "" {
		env.Logger.ErrorContext(ctx, "NimbusStorageClass not defined in config")
		return PostDeploy500JSONResponse{
			Status:  apierror.InternalServerError.Status(),
			Code:    apierror.InternalServerError.String(),
			Message: "Internal Server Error",
			ErrorId: requestID,
		}, nil
	}

	// Read File
	env.Logger.DebugContext(ctx, "retrieving form from file")
	files := form.File["file"]
	if len(files) == 0 {
		env.Logger.ErrorContext(ctx, "no files in form")
		return PostDeploy400JSONResponse{
			Status:  apierror.BadRequest.Status(),
			Code:    apierror.BadRequest.String(),
			Message: "file not found in form",
			ErrorId: requestID,
		}, nil
	}
	fileheader := files[0]
	env.Logger.DebugContext(ctx, "found file", slog.String("filename", fileheader.Filename))

	env.Logger.DebugContext(ctx, "reading file content", slog.String("filename", fileheader.Filename))
	file, err := fileheader.Open()
	if err != nil {
		env.Logger.ErrorContext(ctx, "failed to open file",
			slog.String("filename", fileheader.Filename),
			slog.Any("error", err))
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
		env.Logger.ErrorContext(ctx, "failed to read file",
			slog.String("filename", fileheader.Filename),
			slog.Any("error", err))
		return PostDeploy500JSONResponse{
			Status:  apierror.InternalServerError.Status(),
			Code:    apierror.InternalServerError.String(),
			Message: "Internal Server Error",
			ErrorId: requestID,
		}, nil
	}

	env.Logger.DebugContext(ctx, "unmarshaling yaml", slog.String("filename", fileheader.Filename))
	var config models.Config
	err = yaml.Unmarshal(content, &config)
	if err != nil {
		env.Logger.ErrorContext(ctx, "failed to unmarshal yaml",
			slog.String("filename", fileheader.Filename),
			slog.Any("error", err))
		return PostDeploy400JSONResponse{
			Status:  apierror.BadRequest.Status(),
			Code:    apierror.BadRequest.String(),
			Message: "failed to parse file - invalid yaml",
			ErrorId: requestID,
		}, nil
	}
	if config.AppName == "" {
		env.Logger.ErrorContext(ctx, "app name is missing in config",
			slog.String("filename", fileheader.Filename))
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
		env.Logger.DebugContext(ctx, "defaulting AllowBranchPreviews to true",
			slog.String("app", config.AppName))
	}

	// Retrieve project
	var deployRequest models.DeployRequest
	env.Logger.DebugContext(
		ctx, "retrieving project by name", slog.String("name", config.AppName))
	project, err := env.Database.GetProjectByName(ctx, config.AppName)
	if errors.Is(err, pgx.ErrNoRows) {
		env.Logger.ErrorContext(ctx, "project not found",
			slog.String("app", config.AppName),
			slog.Any("error", err))
		return PostDeploy404JSONResponse{
			Status:  apierror.ProjectNotFound.Status(),
			Code:    apierror.ProjectNotFound.String(),
			Message: "project with app name not found",
			ErrorId: requestID,
		}, nil
	} else if err != nil {
		env.Logger.ErrorContext(ctx, "failed to get project",
			slog.String("app", config.AppName),
			slog.Any("error", err))
		return PostDeploy500JSONResponse{
			Status:  apierror.InternalServerError.Status(),
			Code:    apierror.InternalServerError.String(),
			Message: "Internal Server Error",
			ErrorId: requestID,
		}, nil
	}
	deployRequest.ProjectID = project.ID
	env.Logger.DebugContext(ctx, "project retrieved",
		slog.String("project", project.Name),
		slog.String("project_id", project.ID.String()))

	// Check user permissions
	env.Logger.DebugContext(ctx, "checking user project access")
	user := database.UserFromContext(ctx)
	authorized, err := env.Database.IsUserInProject(ctx, database.IsUserInProjectParams{
		UserID:    user.ID,
		ProjectID: project.ID,
	})
	if err != nil {
		env.Logger.ErrorContext(ctx, "failed to check user access",
			slog.String("project", project.Name),
			slog.String("project_id", project.ID.String()),
			slog.Any("error", err))
		return PostDeploy500JSONResponse{
			Status:  apierror.InternalServerError.Status(),
			Code:    apierror.InternalServerError.String(),
			Message: "Internal Server Error",
			ErrorId: requestID,
		}, nil
	}
	if !authorized {
		env.Logger.DebugContext(ctx, "user is not authorized to deploy project",
			slog.String("project", project.Name),
			slog.String("user_id", user.ID.String()))
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
	env.Logger.DebugContext(ctx, "getting project services",
		slog.String("project", project.Name),
		slog.String("branch", deployRequest.BranchName))
	servicesList, err := env.Database.GetServicesByProject(ctx, database.GetServicesByProjectParams{
		ProjectID:     deployRequest.ProjectID,
		ProjectBranch: deployRequest.BranchName,
	})
	if err != nil {
		env.Logger.ErrorContext(ctx, "failed to get project services",
			slog.String("project", project.Name),
			slog.String("branch", deployRequest.BranchName),
			slog.Any("error", err))
		return PostDeploy500JSONResponse{
			Status:  apierror.InternalServerError.Status(),
			Code:    apierror.InternalServerError.String(),
			Message: "Internal Server Error",
			ErrorId: requestID,
		}, nil
	}
	deployRequest.ExistingServices = servicesList

	// Apply project secrets
	env.Logger.DebugContext(ctx, "applying project secrets",
		slog.String("project", project.Name))
	secrets, err := kubernetes.GetSecretValues(ctx, project.Name, env)
	if err != nil {
		env.Logger.ErrorContext(ctx, "failed to get secret values",
			slog.String("project", project.Name),
			slog.Any("error", err))
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
	env.Logger.DebugContext(
		ctx, "validating namespace", slog.String("namespace", deployRequest.Namespace))
	created, err := kubernetes.ValidateNamespace(ctx, deployRequest.Namespace, env)
	if err != nil {
		env.Logger.ErrorContext(ctx, "failed to validate namespace",
			slog.String("namespace", deployRequest.Namespace),
			slog.Any("error", err))
		return PostDeploy500JSONResponse{
			Status:  apierror.InternalServerError.Status(),
			Code:    apierror.InternalServerError.String(),
			Message: "Internal Server Error",
			ErrorId: requestID,
		}, nil
	}
	if created && deployRequest.BranchName != "main" && deployRequest.BranchName != "master" {
		mainNS := utils.GetSanitizedNamespace(config.AppName, "main")
		vals, err := kubernetes.GetSecretValues(ctx, mainNS, env)
		if err != nil {
			env.Logger.ErrorContext(ctx, "failed to get secret values",
				slog.String("namespace", mainNS),
				slog.String("source", "main"),
				slog.Any("error", err))
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
				vals, env)
			if err != nil {
				env.Logger.ErrorContext(ctx, "failed to update secrets",
					slog.String("namespace", deployRequest.Namespace),
					slog.String("app", config.AppName),
					slog.Any("error", err))
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
	env.Logger.DebugContext(ctx, "deleting services not present in config",
		slog.String("project", project.Name),
		slog.String("branch", deployRequest.BranchName))
	serviceNames := make(map[string]bool)
	for _, service := range config.Services {
		if serviceNames[service.Name] {
			env.Logger.ErrorContext(ctx, "duplicate service name", slog.String("service", service.Name))
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
			env.Logger.DebugContext(
				ctx, "deleting deployment",
				slog.String("service", service.ServiceName),
				slog.String("namespace", deployRequest.Namespace))
			err := kubernetes.DeleteDeployment(
				ctx, deployRequest.Namespace, service.ServiceName, env)
			if err != nil {
				env.Logger.ErrorContext(ctx, "failed to delete deployment",
					slog.String("service", service.ServiceName),
					slog.String("namespace", deployRequest.Namespace),
					slog.Any("error", err))
				return PostDeploy500JSONResponse{
					Status:  apierror.InternalServerError.Status(),
					Code:    apierror.InternalServerError.String(),
					Message: "Internal Server Error",
					ErrorId: requestID,
				}, nil
			}

			env.Logger.DebugContext(ctx, "deleting service",
				slog.String("service", service.ServiceName),
				slog.String("namespace", deployRequest.Namespace))
			err = kubernetes.DeleteService(ctx, deployRequest.Namespace, service.ServiceName, env)
			if err != nil {
				env.Logger.ErrorContext(ctx, "failed to delete service",
					slog.String("service", service.ServiceName),
					slog.String("namespace", deployRequest.Namespace),
					slog.Any("error", err))
				return PostDeploy500JSONResponse{
					Status:  apierror.InternalServerError.Status(),
					Code:    apierror.InternalServerError.String(),
					Message: "Internal Server Error",
					ErrorId: requestID,
				}, nil
			}

			if service.Ingress.Valid {
				env.Logger.DebugContext(ctx, "deleting ingress",
					slog.String("service", service.ServiceName),
					slog.String("ingress", service.Ingress.String))
				err = kubernetes.DeleteIngress(ctx, deployRequest.Namespace, service.Ingress.String, env)
				if err != nil {
					env.Logger.ErrorContext(ctx, "failed to delete ingress",
						slog.String("service", service.ServiceName),
						slog.String("ingress", service.Ingress.String),
						slog.Any("error", err))
					return PostDeploy500JSONResponse{
						Status:  apierror.InternalServerError.Status(),
						Code:    apierror.InternalServerError.String(),
						Message: "Internal Server Error",
						ErrorId: requestID,
					}, nil
				}
			}

			env.Logger.DebugContext(ctx, "deleting service in database",
				slog.String("service", service.ServiceName))
			err = env.Database.DeleteServiceById(ctx, service.ID)
			if err != nil {
				env.Logger.ErrorContext(ctx, "failed to delete service",
					slog.String("service", service.ServiceName),
					slog.Any("error", err))
				return PostDeploy500JSONResponse{
					Status:  apierror.InternalServerError.Status(),
					Code:    apierror.InternalServerError.String(),
					Message: "Internal Server Error",
					ErrorId: requestID,
				}, nil
			}
		}
	}

	env.Logger.DebugContext(ctx, "creating services and deployments",
		slog.String("project", project.Name),
		slog.String("branch", deployRequest.BranchName))
	serviceUrls := make(map[string][]string)
	for _, serviceConfig := range config.Services {

		// Create deployment
		env.Logger.DebugContext(ctx, "creating deployment",
			slog.String("service", serviceConfig.Name))
		deploymentSpec, err := kubernetes.GenerateDeploymentSpec(
			ctx, &deployRequest, &serviceConfig, env)
		if err != nil {
			env.Logger.ErrorContext(ctx, "failed to create deployment",
				slog.String("service", serviceConfig.Name),
				slog.Any("error", err))
			return PostDeploy500JSONResponse{
				Status:  apierror.InternalServerError.Status(),
				Code:    apierror.InternalServerError.String(),
				Message: "Internal Server Error",
				ErrorId: requestID,
			}, nil
		}
		_, err = kubernetes.CreateDeployment(ctx, deployRequest.Namespace, deploymentSpec, env)
		if err != nil {
			env.Logger.ErrorContext(ctx, "failed to create deployment",
				slog.String("service", serviceConfig.Name),
				slog.Any("error", err))
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
			env.Logger.DebugContext(ctx, "creating service",
				slog.String("service", serviceConfig.Name))
			serviceSpec, err := kubernetes.GenerateServiceSpec(deployRequest.Namespace, &serviceConfig, oldService)
			if err != nil {
				env.Logger.ErrorContext(ctx, "failed to generate service spec",
					slog.String("service", serviceConfig.Name),
					slog.Any("error", err))
				return PostDeploy500JSONResponse{
					Status:  apierror.InternalServerError.Status(),
					Code:    apierror.InternalServerError.String(),
					Message: "Internal Server Error",
					ErrorId: requestID,
				}, nil
			}
			kubeSvc, err = kubernetes.CreateService(ctx, deployRequest.Namespace, serviceSpec, env)
			if err != nil {
				env.Logger.ErrorContext(ctx, "failed to create service",
					slog.String("service", serviceConfig.Name),
					slog.Any("error", err))
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
			env.Logger.DebugContext(ctx, "creating service in database",
				slog.String("service", serviceConfig.Name))
			newSvc, err = env.Database.CreateService(ctx, database.CreateServiceParams{
				ID:            uuid.New(),
				ProjectID:     deployRequest.ProjectID,
				ProjectBranch: deployRequest.BranchName,
				ServiceName:   serviceConfig.Name,
			})
			if err != nil {
				env.Logger.ErrorContext(ctx, "failed to create service in database",
					slog.String("service", serviceConfig.Name),
					slog.Any("error", err))
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

		env.Logger.DebugContext(ctx, "updating service networking in database",
			slog.String("service", serviceConfig.Name))
		if kubeSvc == nil {
			if serviceConfig.Template != "http" {
				env.Logger.DebugContext(ctx, "no service ports specified - clearing node ports",
					slog.String("service", serviceConfig.Name))
				err := env.Database.SetServiceNodePorts(ctx, database.SetServiceNodePortsParams{
					ID:        serviceID,
					NodePorts: []int32{},
				})
				if err != nil {
					env.Logger.ErrorContext(ctx, "failed to clear service ports",
						slog.String("service", serviceConfig.Name),
						slog.Any("error", err))
					return PostDeploy500JSONResponse{
						Status:  apierror.InternalServerError.Status(),
						Code:    apierror.InternalServerError.String(),
						Message: "Internal Server Error",
						ErrorId: requestID,
					}, nil
				}
			} else {
				if existingIngress != nil {
					env.Logger.DebugContext(ctx, "removing existing ingress",
						slog.String("service", serviceConfig.Name),
						slog.String("ingress", *existingIngress))
					err = kubernetes.DeleteIngress(ctx, deployRequest.Namespace, *existingIngress, env)
					if err != nil {
						env.Logger.ErrorContext(ctx, "failed to delete ingress",
							slog.String("service", serviceConfig.Name),
							slog.String("ingress", *existingIngress),
							slog.Any("error", err))
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
					env.Logger.ErrorContext(ctx, "failed to set ingress",
						slog.String("service", serviceConfig.Name),
						slog.Any("error", err))
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
				env.Logger.DebugContext(ctx, "clearing node ports for private service",
					slog.String("service", serviceConfig.Name))
				err := env.Database.SetServiceNodePorts(ctx, database.SetServiceNodePortsParams{
					ID:        serviceID,
					NodePorts: []int32{},
				})
				if err != nil {
					env.Logger.ErrorContext(ctx, "failed to update service in database",
						slog.String("service", serviceConfig.Name),
						slog.Any("error", err))
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
			env.Logger.DebugContext(ctx, "retrieving node ports from spec",
				slog.String("service", serviceConfig.Name))
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
					Status:  apierror.InternalServerError.Status(),
					Code:    apierror.InternalServerError.String(),
					Message: "Internal Server Error",
					ErrorId: requestID,
				}, nil
			}
		} else {
			if !serviceConfig.Public {
				if existingIngress != nil {
					env.Logger.DebugContext(ctx, "deleting existing ingress for private service",
						slog.String("service", serviceConfig.Name),
						slog.String("ingress", *existingIngress))
					err = kubernetes.DeleteIngress(ctx, deployRequest.Namespace, *existingIngress, env)
					if err != nil {
						env.Logger.ErrorContext(ctx, "failed to delete ingress",
							slog.String("service", serviceConfig.Name),
							slog.String("ingress", *existingIngress),
							slog.Any("error", err))
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
					env.Logger.ErrorContext(ctx, "failed to set service ingress",
						slog.String("service", serviceConfig.Name),
						slog.Any("error", err))
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

			env.Logger.DebugContext(ctx, "creating ingress for service",
				slog.String("service", serviceConfig.Name))
			ingressSpec, err := kubernetes.GenerateIngressSpec(deployRequest.Namespace, &serviceConfig, existingIngress, env)
			if err != nil {
				env.Logger.ErrorContext(ctx, "failed to create ingress for service",
					slog.String("service", serviceConfig.Name),
					slog.Any("error", err))
				return PostDeploy500JSONResponse{
					Status:  apierror.InternalServerError.Status(),
					Code:    apierror.InternalServerError.String(),
					Message: "Internal Server Error",
					ErrorId: requestID,
				}, nil
			}
			newIngress, err := kubernetes.CreateIngress(ctx, deployRequest.Namespace, ingressSpec, env)
			if err != nil {
				env.Logger.ErrorContext(ctx, "failed to create ingress",
					slog.String("service", serviceConfig.Name),
					slog.Any("error", err))
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
				env.Logger.ErrorContext(ctx, "failed to update ingress in database",
					slog.String("service", serviceConfig.Name),
					slog.String("ingress_host", newIngress.Spec.Rules[0].Host),
					slog.Any("error", err))
				return PostDeploy500JSONResponse{
					Status:  apierror.InternalServerError.Status(),
					Code:    apierror.InternalServerError.String(),
					Message: "Internal Server Error",
					ErrorId: requestID,
				}, nil
			}
			urls = append(urls, fmt.Sprintf("https://%s", newIngress.Spec.Rules[0].Host))
		}

		env.Logger.DebugContext(ctx, "successfully created service",
			slog.String("service", serviceConfig.Name))
		serviceUrls[serviceConfig.Name] = urls
	}

	return PostDeploy200JSONResponse{
		Services: serviceUrls,
	}, nil
}
