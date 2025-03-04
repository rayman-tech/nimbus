package handlers

import (
	"nimbus/internal/services"

	"io"
	"log"
	"net/http"

	"gopkg.in/yaml.v3"
)

type Config struct {
	App      string    `yaml:"app"`
	Services []Service `yaml:"services"`
}

type Service struct {
	Name     string        `yaml:"name"`
	Image    string        `yaml:"image,omitempty"`
	Network  Network       `yaml:"network,omitempty"`
	Template string        `yaml:"template,omitempty"`
	Version  string        `yaml:"version,omitempty"`
	Configs  []ConfigEntry `yaml:"configs,omitempty"`
}

type Network struct {
	Ports []int `yaml:"ports"`
}

type ConfigEntry struct {
	Path  string `yaml:"path"`
	Value string `yaml:"value"`
}

func Deploy(w http.ResponseWriter, r *http.Request) {
	log.Println("POST /deploy")

	err := r.ParseMultipartForm(512 << 20)
	if err != nil {
		http.Error(w, "Failed to parse multipart form", http.StatusBadRequest)
		return
	}

	file, handler, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "Error retrieving the file", http.StatusBadRequest)
		return
	}

	defer file.Close()

	log.Printf("Received file: %s\n", handler.Filename)

	content, err := io.ReadAll(file)
	if err != nil {
		http.Error(w, "Error reading file", http.StatusInternalServerError)
		return
	}

	var config Config
	err = yaml.Unmarshal(content, &config)
	if err != nil {
		http.Error(w, "Error parsing YAML", http.StatusBadRequest)
		return
	}
	log.Printf("Parsed YAML: %+v\n", config)

	namespace, err := services.GetNamespace(config.App)
	if err != nil {
		http.Error(w, "Error retrieving namespace", http.StatusInternalServerError)
		return
	}

	if namespace == nil {
		err = services.CreateNamespace(config.App)
		if err != nil {
			http.Error(w, "Error creating namespace", http.StatusInternalServerError)
			return
		}
		log.Printf("Created namespace: %s\n", config.App)
	}

	w.WriteHeader(http.StatusOK)
}
