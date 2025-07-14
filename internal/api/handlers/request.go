package handlers

import (
	"nimbus/internal/database"
	nimbusEnv "nimbus/internal/env"
	"nimbus/internal/kubernetes"
	"nimbus/internal/logging"
	"nimbus/internal/models"
	"strings"

	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"

	"gopkg.in/yaml.v3"
)

func buildDeployRequest(w http.ResponseWriter, r *http.Request, env *nimbusEnv.Env, ctx context.Context) (*models.DeployRequest, context.Context, error) {
	env.Logger.DebugContext(ctx, "Parsing form")
	err := r.ParseMultipartForm(512 << 20)
	if err != nil {
		env.Logger.LogAttrs(
			ctx, slog.LevelError,
			"Failed to parse multipart form", slog.Any("error", err),
		)
		http.Error(w, "Failed to parse multipart form", http.StatusBadRequest)
		return nil, nil, err
	}

	env.Logger.DebugContext(ctx, "Reading API key")
	apiKey := r.Header.Get(xApiKey)
	if apiKey == "" {
		env.Logger.ErrorContext(ctx, "API key missing")
		http.Error(w, "API key missing", http.StatusUnauthorized)
		return nil, nil, fmt.Errorf("API key missing")
	}

	if os.Getenv("NIMBUS_STORAGE_CLASS") == "" {
		env.Logger.ErrorContext(ctx, "NIMBUS_STORAGE_CLASS environment variable not set")
		http.Error(w, "Server is missing environment variables", http.StatusInternalServerError)
		return nil, nil, fmt.Errorf("NIMBUS_STORAGE_CLASS environment variable not set")
	}

	env.Logger.DebugContext(ctx, "Retrieving file from form")
	file, handler, err := r.FormFile(formFile)
	if err != nil {
		env.Logger.LogAttrs(
			ctx, slog.LevelError,
			"Error retrieving file", slog.Any("error", err),
		)
		http.Error(w, "Error retrieving the file", http.StatusBadRequest)
		return nil, nil, err
	}

	defer file.Close()
	logging.AppendCtx(ctx, slog.String("filename", handler.Filename))
	env.Logger.DebugContext(ctx, "File received")

	env.Logger.DebugContext(ctx, "Reading file content")
	content, err := io.ReadAll(file)
	if err != nil {
		env.Logger.LogAttrs(
			ctx, slog.LevelError,
			"Error reading file", slog.Any("error", err),
		)
		http.Error(w, "Error reading file", http.StatusInternalServerError)
		return nil, nil, err
	}

	env.Logger.DebugContext(ctx, "Unmarshaling YAML file")
	var config models.Config
	err = yaml.Unmarshal(content, &config)
	if err != nil {
		env.Logger.LogAttrs(
			ctx, slog.LevelError,
			"Error parsing YAML", slog.Any("error", err),
		)
		http.Error(w, "Error parsing YAML", http.StatusBadRequest)
		return nil, nil, err
	}

	env.Logger.DebugContext(ctx, "Retrieving user by API key")
	user, err := env.Database.GetUserByApiKey(r.Context(), apiKey)
	if err != nil {
		env.Logger.LogAttrs(
			ctx, slog.LevelError,
			"Error getting user by API key", slog.Any("error", err),
		)
		http.Error(w, "Invalid API key", http.StatusUnauthorized)
		return nil, nil, err
	}

	if config.AppName == "" {
		env.Logger.ErrorContext(ctx, "App name missing in config")
		http.Error(w, "app field is required", http.StatusBadRequest)
		return nil, nil, fmt.Errorf("app field missing")
	}

	env.Logger.DebugContext(ctx, "Retrieving project by name")
	project, err := env.Database.GetProjectByName(r.Context(), config.AppName)
	if err != nil {
		env.Logger.LogAttrs(
			ctx, slog.LevelError,
			"Error getting project by name", slog.Any("error", err),
		)
		http.Error(w, "Project not found", http.StatusBadRequest)
		return nil, nil, err
	}

	env.Logger.DebugContext(ctx, "Checking user project access")
	authorized, err := env.Database.IsUserInProject(r.Context(), database.IsUserInProjectParams{
		UserID:    user.ID,
		ProjectID: project.ID,
	})
	if err != nil || !authorized {
		if err != nil {
			env.Logger.LogAttrs(ctx, slog.LevelError, "Error checking user project access", slog.Any("error", err))
		}
		http.Error(w, "User not authorized for project", http.StatusUnauthorized)
		return nil, nil, fmt.Errorf("user not authorized")
	}
	ctx = logging.AppendCtx(ctx, slog.String("app", project.Name))

	branch := r.FormValue(formBranch)
	ctx = logging.AppendCtx(ctx, slog.String("branch", branch))

	env.Logger.DebugContext(ctx, "Retrieving project services")
	existingServices, err := env.Database.GetServicesByProject(r.Context(), database.GetServicesByProjectParams{
		ProjectID:     project.ID,
		ProjectBranch: branch,
	})
	if err != nil {
		env.Logger.LogAttrs(
			ctx, slog.LevelError,
			"Error retrieving project services", slog.Any("error", err),
		)
		http.Error(w, "Error getting project services", http.StatusInternalServerError)
		return nil, nil, err
	}

	// SPECIFY WHETHER TO USE NAME GIVEN IN YAML OR PROJECT NAME IN THE DATABASE
	namespace := project.Name
	replacer := strings.NewReplacer(
		"/", "-",
		"_", "-",
		" ", "-",
		"#", "",
		"!", "",
		"@", "",
		".", "",
	)
	if branch != "main" && branch != "master" {
		namespace = fmt.Sprintf("%s-%s", project.Name, replacer.Replace(branch))
	}

	env.Logger.DebugContext(ctx, "Applying project secrets")
	secrets, err := kubernetes.GetSecretValues(namespace, kubernetes.ProjectSecretName, env)
	if err == nil {
		for i := range config.Services {
			for j := range config.Services[i].Env {
				val := config.Services[i].Env[j].Value
				if strings.HasPrefix(val, "${") && strings.HasSuffix(val, "}") {
					key := strings.TrimSuffix(strings.TrimPrefix(val, "${"), "}")
					if secretVal, ok := secrets[key]; ok {
						config.Services[i].Env[j].Value = secretVal
					}
				}
			}
		}
	}

	env.Logger.DebugContext(ctx, "Constructing request struct")
	return &models.DeployRequest{
		Namespace:        namespace,
		ProjectID:        project.ID,
		BranchName:       branch,
		ProjectConfig:    config,
		FileContent:      content,
		ExistingServices: existingServices,
	}, ctx, nil
}
