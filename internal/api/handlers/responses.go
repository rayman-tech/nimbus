package handlers

type deployResponse struct {
	Urls map[string][]string `json:"services"`
}
