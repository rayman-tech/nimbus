package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"slices"

	"nimbus/internal/database"
	nimbusEnv "nimbus/internal/env"
	"nimbus/internal/kubernetes"
	"nimbus/internal/utils"

	"github.com/gorilla/mux"
)

const (
	xApiKey = "X-API-Key"
)

const envKey = "env"

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

	authorized, err := env.Database.IsUserInProject(
		r.Context(), database.IsUserInProjectParams{
			UserID: user.ID, ProjectID: project.ID,
		})
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
	if !slices.Contains(branches, "main") && !slices.Contains(branches, "master") {
		branches = append(branches, "main")
	}

	for _, branch := range branches {
		namespace := utils.GetSanitizedNamespace(project.Name, branch)
		env.Logger.DebugContext(r.Context(), "Updating secrets for namespace", slog.String("namespace", namespace))
		if err := kubernetes.UpdateSecret(
			r.Context(), namespace, fmt.Sprintf("%s-env", project.Name), req.Secrets, env,
		); err != nil {
			env.Logger.ErrorContext(context.Background(), err.Error())
			http.Error(w, "error updating secrets", http.StatusInternalServerError)
			return
		}
	}
	w.WriteHeader(http.StatusOK)
}
