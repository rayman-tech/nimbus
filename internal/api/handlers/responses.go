package handlers

type secretsNamesResponse struct {
	Secrets []string `json:"secrets"`
}

type secretsValuesResponse struct {
	Secrets map[string]string `json:"secrets"`
}
