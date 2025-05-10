package handlers

import (
	"nimbus/internal/database"
	nimbusEnv "nimbus/internal/env"
	"nimbus/internal/logging"
	"nimbus/internal/models"
	"strings"

	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"

	"github.com/google/uuid"
	"gopkg.in/yaml.v3"
)

type deployRequest struct {
	Namespace        string
	ProjectID        uuid.UUID
	BranchName       string
	ProjectConfig    models.Config
	FileContent      []byte
	ExistingServices []database.Service
}

func buildDeployRequest(w http.ResponseWriter, r *http.Request, env *nimbusEnv.Env, ctx context.Context) (*deployRequest, context.Context, error) {
	env.DebugContext(ctx, "Parsing form")
	err := r.ParseMultipartForm(512 << 20)
	if err != nil {
		env.LogAttrs(
			ctx, slog.LevelError,
			"Failed to parse multipart form", slog.Any("error", err),
		)
		http.Error(w, "Failed to parse multipart form", http.StatusBadRequest)
		return nil, nil, err
	}

	env.DebugContext(ctx, "Reading API key")
	apiKey := r.Header.Get(xApiKey)
	if apiKey == "" {
		env.ErrorContext(ctx, "API key missing")
		http.Error(w, "API key missing", http.StatusUnauthorized)
		return nil, nil, fmt.Errorf("API key missing")
	}

	if os.Getenv("NIMBUS_STORAGE_CLASS") == "" {
		env.ErrorContext(ctx, "NIMBUS_STORAGE_CLASS environment variable not set")
		http.Error(w, "Server is missing environment variables", http.StatusInternalServerError)
		return nil, nil, fmt.Errorf("NIMBUS_STORAGE_CLASS environment variable not set")
	}

	env.DebugContext(ctx, "Retrieving file from form")
	file, handler, err := r.FormFile(formFile)
	if err != nil {
		env.LogAttrs(
			ctx, slog.LevelError,
			"Error retrieving file", slog.Any("error", err),
		)
		http.Error(w, "Error retrieving the file", http.StatusBadRequest)
		return nil, nil, err
	}

	defer file.Close()
	logging.AppendCtx(ctx, slog.String("filename", handler.Filename))
	env.DebugContext(ctx, "File received")

	env.DebugContext(ctx, "Reading file content")
	content, err := io.ReadAll(file)
	if err != nil {
		env.LogAttrs(
			ctx, slog.LevelError,
			"Error reading file", slog.Any("error", err),
		)
		http.Error(w, "Error reading file", http.StatusInternalServerError)
		return nil, nil, err
	}

	env.DebugContext(ctx, "Unmarshaling YAML file")
	var config models.Config
	err = yaml.Unmarshal(content, &config)
	if err != nil {
		env.LogAttrs(
			ctx, slog.LevelError,
			"Error parsing YAML", slog.Any("error", err),
		)
		http.Error(w, "Error parsing YAML", http.StatusBadRequest)
		return nil, nil, err
	}

	env.DebugContext(ctx, "Retrieving project by API key")
	project, err := env.GetProjectByApiKey(r.Context(), apiKey)
	if err != nil {
		env.LogAttrs(
			ctx, slog.LevelError,
			"Error getting project by API key", slog.Any("error", err),
		)
		http.Error(w, "Error getting project", http.StatusUnauthorized)
		return nil, nil, err
	}

	env.DebugContext(ctx, "Validating project name")
	// AppName is optional
	if config.AppName != "" && config.AppName != project.Name {
		env.LogAttrs(
			ctx, slog.LevelError,
			"App name does not match project name", slog.String("app", project.Name),
			slog.String("project", project.Name),
		)
		http.Error(w, "App name does not match project name", http.StatusBadRequest)
		return nil, nil, fmt.Errorf("app name does not match project name")
	}
	ctx = logging.AppendCtx(ctx, slog.String("app", project.Name))

	branch := r.FormValue(formBranch)
	ctx = logging.AppendCtx(ctx, slog.String("branch", branch))

	env.DebugContext(ctx, "Retrieving project services")
	existingServices, err := env.GetServicesByProject(r.Context(), database.GetServicesByProjectParams{
		ProjectID:     project.ID.String(),
		ProjectBranch: branch,
	})
	if err != nil {
		env.LogAttrs(
			ctx, slog.LevelError,
			"Error retrieving project services", slog.Any("error", err),
		)
		http.Error(w, "Error getting project services", http.StatusInternalServerError)
		return nil, nil, err
	}

	// SPECIFY WHETHER TO USE NAME GIVEN IN YAML OR PROJECT NAME IN THE DATABASE
	namespace := project.Name
	if branch != "main" && branch != "master" {
		namespace = fmt.Sprintf("%s-%s", project.Name, strings.ReplaceAll(branch, "/", "-"))
	}

	env.DebugContext(ctx, "Constructing request struct")
	return &deployRequest{
		Namespace:        namespace,
		ProjectID:        project.ID,
		BranchName:       branch,
		ProjectConfig:    config,
		FileContent:      content,
		ExistingServices: existingServices,
	}, ctx, nil
}
