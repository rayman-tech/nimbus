package handlers

import (
	"io"
	"log"
	"net/http"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Name  string `yaml:"name"`
	Image string `yaml:"image"`
	Ports []int  `yaml:"ports"`
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

	w.WriteHeader(http.StatusOK)
}
