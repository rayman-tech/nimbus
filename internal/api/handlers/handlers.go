package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"

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

// StreamLogs streams logs for the first pod of a given service.
func StreamLogs(w http.ResponseWriter, r *http.Request) {
	env, ok := r.Context().Value(envKey).(*nimbusEnv.Env)
	if !ok {
		env = nimbusEnv.Null()
	}

	vars := mux.Vars(r)
	name := vars["name"]
	projectName := r.URL.Query().Get("project")
	branch := utils.GetBranch(r)

	apiKey := r.Header.Get(xApiKey)
	project, _, err := utils.AuthorizeProject(r.Context(), env, apiKey, projectName)
	if err != nil {
		if strings.Contains(err.Error(), "project not found") {
			http.Error(w, "project not found", http.StatusNotFound)
			return
		}
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	namespace := utils.GetSanitizedNamespace(project.Name, branch)
	stream, err := kubernetes.StreamServiceLogs(r.Context(), namespace, name, env)
	if err != nil {
		env.Logger.ErrorContext(context.Background(), err.Error())
		http.Error(w, "error streaming logs", http.StatusInternalServerError)
		return
	}
	defer func() { _ = stream.Close() }()

	w.Header().Set("Content-Type", "text/plain")

	const bufLen = 1024
	flusher, _ := w.(http.Flusher)
	buf := make([]byte, bufLen)
	for {
		n, err := stream.Read(buf)
		if n > 0 {
			_, _ = w.Write(buf[:n])
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

	authorized, err := env.Database.IsUserInProject(
		r.Context(),
		database.IsUserInProjectParams{UserID: user.ID, ProjectID: project.ID})
	if err != nil || !authorized {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	services, err := env.Database.GetServicesByProject(
		r.Context(),
		database.GetServicesByProjectParams{ProjectID: project.ID, ProjectBranch: branch})
	if err != nil {
		env.Logger.ErrorContext(r.Context(), err.Error())
		http.Error(w, "error fetching services", http.StatusInternalServerError)
		return
	}

	namespace := utils.GetSanitizedNamespace(project.Name, branch)
	for _, svc := range services {
		err = kubernetes.DeleteDeployment(r.Context(), namespace, svc.ServiceName, env)
		if err != nil {
			env.Logger.ErrorContext(r.Context(), "failed to delete deployment", slog.Any("error", err))
		}
		err = kubernetes.DeleteService(r.Context(), namespace, svc.ServiceName, env)
		if err != nil {
			env.Logger.ErrorContext(r.Context(), "failed to delete service", slog.Any("error", err))
		}
		if svc.Ingress.Valid {
			err = kubernetes.DeleteIngress(r.Context(), namespace, svc.Ingress.String, env)
			if err != nil {
				env.Logger.ErrorContext(r.Context(), "failed to delete ingress", slog.Any("error", err))
			}
		}
		err = env.Database.DeleteServiceById(r.Context(), svc.ID)
		if err != nil {
			env.Logger.ErrorContext(r.Context(), "failed to delete service", slog.Any("error", err))
			http.Error(w, "error deleting service", http.StatusInternalServerError)
			return
		}
	}

	ids, err := env.Database.GetUnusedVolumeIdentifiers(
		r.Context(),
		database.GetUnusedVolumeIdentifiersParams{
			ProjectID: project.ID, ProjectBranch: branch, Column3: []string{},
		})
	if err == nil {
		for _, id := range ids {
			err = kubernetes.DeletePVC(namespace, fmt.Sprintf("pvc-%s", id.String()), env)
			if err != nil {
				env.Logger.ErrorContext(r.Context(), "failed to delete pvc", slog.Any("error", err))
			}
		}
	}
	err = env.Database.DeleteUnusedVolumes(r.Context(),
		database.DeleteUnusedVolumesParams{
			ProjectID: project.ID, ProjectBranch: branch, Column3: []string{},
		})
	if err != nil {
		env.Logger.ErrorContext(r.Context(), "failed to delete unused volumes", slog.Any("error", err))
		http.Error(w, "error deleting unused volumes", http.StatusInternalServerError)
		return
	}

	err = kubernetes.DeleteNamespace(namespace, env)
	if err != nil {
		env.Logger.ErrorContext(r.Context(), "failed to delete namespace", slog.Any("error", err))
		http.Error(w, "error deleting namespace", http.StatusInternalServerError)
	}
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

	authorized, err := env.Database.IsUserInProject(
		r.Context(), database.IsUserInProjectParams{UserID: user.ID, ProjectID: project.ID})
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

	for _, branch := range branches {
		services, err := env.Database.GetServicesByProject(
			r.Context(),
			database.GetServicesByProjectParams{ProjectID: project.ID, ProjectBranch: branch})
		if err != nil {
			env.Logger.ErrorContext(context.Background(), err.Error())
			continue
		}

		namespace := utils.GetSanitizedNamespace(project.Name, branch)
		for _, svc := range services {
			err = kubernetes.DeleteDeployment(r.Context(), namespace, svc.ServiceName, env)
			if err != nil {
				env.Logger.ErrorContext(r.Context(), "failed to delete deployment", slog.Any("error", err))
			}
			err = kubernetes.DeleteService(r.Context(), namespace, svc.ServiceName, env)
			if err != nil {
				env.Logger.ErrorContext(r.Context(), "failed to deleted service", slog.Any("error", err))
			}
			if svc.Ingress.Valid {
				err = kubernetes.DeleteIngress(r.Context(), namespace, svc.Ingress.String, env)
				if err != nil {
					env.Logger.ErrorContext(r.Context(), "failed to delete ingress", slog.Any("error", err))
				}
			}
			err = env.Database.DeleteServiceById(r.Context(), svc.ID)
			if err != nil {
				env.Logger.ErrorContext(r.Context(), "failed to delete service", slog.Any("error", err))
			}
		}

		ids, err := env.Database.GetUnusedVolumeIdentifiers(
			r.Context(),
			database.GetUnusedVolumeIdentifiersParams{
				ProjectID: project.ID, ProjectBranch: branch, Column3: []string{},
			})
		if err == nil {
			for _, id := range ids {
				err = kubernetes.DeletePVC(namespace, fmt.Sprintf("pvc-%s", id.String()), env)
				if err != nil {
					env.Logger.ErrorContext(r.Context(), "failed to delete pvc", slog.Any("error", err))
				}
			}
		}
		err = env.Database.DeleteUnusedVolumes(
			r.Context(),
			database.DeleteUnusedVolumesParams{
				ProjectID: project.ID, ProjectBranch: branch, Column3: []string{},
			})
		if err != nil {
			env.Logger.ErrorContext(r.Context(), "failed to delete unused volumes", slog.Any("error", err))
			http.Error(w, "error deleting unused volumes", http.StatusInternalServerError)
			return
		}
		err = kubernetes.DeleteNamespace(namespace, env)
		if err != nil {
			env.Logger.ErrorContext(r.Context(), "failed to delete namespace", slog.Any("error", err))
			http.Error(w, "error deleting unused volumes", http.StatusInternalServerError)
			return
		}
	}

	err = env.Database.DeleteProject(r.Context(), project.ID)
	if err != nil {
		env.Logger.ErrorContext(r.Context(), "failed to delete project", slog.Any("error", err))
	}
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

	authorized, err := env.Database.IsUserInProject(
		r.Context(), database.IsUserInProjectParams{
			UserID: user.ID, ProjectID: project.ID,
		})
	if err != nil || !authorized {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	showValues := r.URL.Query().Get("values") == "true"
	var resp any
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
	err = json.NewEncoder(w).Encode(resp)
	if err != nil {
		env.Logger.ErrorContext(r.Context(), "failed to encode response", slog.Any("error", err))
	}
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
