// Package utils contains utility functions
package utils

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"nimbus/internal/database"
	"nimbus/internal/env"
)

func FormatServiceURL(domain string, nodePort int32) string {
	return fmt.Sprintf("%s:%d", domain, nodePort)
}

func GetSanitizedNamespace(namespace, branch string) string {
	sanitizedNamespace := strings.ToLower(namespace)
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
		sanitizedNamespace = fmt.Sprintf("%s-%s", namespace, replacer.Replace(branch))
	}
	return sanitizedNamespace
}

func GetBranch(r *http.Request) string {
	branch := r.URL.Query().Get("branch")
	if branch == "" {
		branch = "main"
	}
	return branch
}

func AuthorizeProject(
	ctx context.Context, env *env.Env, apiKey, projectName string,
) (database.Project, database.User, error) {
	user, err := env.Database.GetUserByApiKey(ctx, apiKey)
	if err != nil {
		return database.Project{}, database.User{}, fmt.Errorf("unauthorized: %w", err)
	}
	project, err := env.Database.GetProjectByName(ctx, projectName)
	if err != nil {
		return database.Project{}, database.User{}, fmt.Errorf("project not found: %w", err)
	}
	authorized, err := env.Database.IsUserInProject(ctx,
		database.IsUserInProjectParams{UserID: user.ID, ProjectID: project.ID})
	if err != nil || !authorized {
		return database.Project{}, database.User{}, fmt.Errorf("unauthorized")
	}
	return project, user, nil
}
