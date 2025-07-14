package handlers

import "nimbus/internal/database"

type deployResponse struct {
	Urls map[string][]string `json:"services"`
}

type projectsResponse struct {
	Projects []database.Project `json:"projects"`
}

type serviceListItem struct {
	ProjectName   string `json:"project"`
	ProjectBranch string `json:"branch"`
	ServiceName   string `json:"name"`
	Status        string `json:"status"`
}

type servicesResponse struct {
	Services []serviceListItem `json:"services"`
}

type podStatus struct {
	Name  string `json:"name"`
	Phase string `json:"phase"`
}

type serviceDetailResponse struct {
	Project     string      `json:"project"`
	Branch      string      `json:"branch"`
	Name        string      `json:"name"`
	NodePorts   []int32     `json:"nodePorts"`
	Ingress     *string     `json:"ingress,omitempty"`
	PodStatuses []podStatus `json:"pods"`
}
