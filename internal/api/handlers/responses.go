package handlers

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
	Logs        string      `json:"logs,omitempty"`
}

type secretsNamesResponse struct {
	Secrets []string `json:"secrets"`
}

type secretsValuesResponse struct {
	Secrets map[string]string `json:"secrets"`
}
