package main

import (
	"nimbus/internal/api"

	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	urllib "net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

func main() {
	var rootCmd = &cobra.Command{Use: "nimbus"}

	var serverCmd = &cobra.Command{
		Use:   "server",
		Short: "Start the server",
		Run: func(cmd *cobra.Command, args []string) {
			port, _ := cmd.Flags().GetString("port")
			api.Start(port, nil)
		},
	}
	serverCmd.Flags().StringP("port", "p", "8080", "Port to run the server on")

	var deployCmd = &cobra.Command{
		Use:   "deploy",
		Short: "Deploy using nimbus.yaml",
		RunE: func(cmd *cobra.Command, args []string) error {
			host, _ := cmd.Flags().GetString("host")
			if host == "" {
				host = os.Getenv("NIMBUS_HOST")
			}
			if host == "" {
				return fmt.Errorf("NIMBUS_HOST environment variable is not set, please provide a host using --host or set NIMBUS_HOST")
			}

			filePath, _ := cmd.Flags().GetString("file")
			if filePath == "" {
				filePath = "./nimbus.yaml"
			}

			apiKey, _ := cmd.Flags().GetString("apikey")
			if apiKey == "" {
				apiKey = os.Getenv("NIMBUS_API_KEY")
			}

			file, err := os.Open(filePath)
			if err != nil {
				return fmt.Errorf("unable to open %s: %w", filePath, err)
			}
			defer file.Close()

			body := &bytes.Buffer{}
			writer := multipart.NewWriter(body)
			part, err := writer.CreateFormFile("file", filepath.Base(filePath))
			if err != nil {
				return err
			}
			if _, err := io.Copy(part, file); err != nil {
				return err
			}

			branchCmd := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD")
			branchOutput, err := branchCmd.Output()
			if err != nil {
				return fmt.Errorf("failed to get current git branch: %w", err)
			}
			branch := strings.TrimSpace(string(branchOutput))

			if err := writer.WriteField("branch", branch); err != nil {
				return fmt.Errorf("failed to add branch field: %w", err)
			}

			writer.Close()

			req, err := http.NewRequest("POST", host+"/deploy", body)
			if err != nil {
				return err
			}
			req.Header.Set("Content-Type", writer.FormDataContentType())
			if apiKey != "" {
				req.Header.Set("X-API-Key", apiKey)
			}

			done := make(chan struct{})
			go func() {
				spinner := []string{"|", "/", "-", "\\"}
				i := 0
				for {
					select {
					case <-done:
						fmt.Print("\r")
						return
					default:
						fmt.Printf("\r%s Processing...", spinner[i%len(spinner)])
						time.Sleep(10 * time.Millisecond)
						i++
					}
				}
			}()

			resp, err := http.DefaultClient.Do(req)
			close(done)
			if err != nil {
				return err
			}
			defer resp.Body.Close()

			data, err := io.ReadAll(resp.Body)
			if err != nil {
				return err
			}

			if resp.StatusCode != http.StatusOK {
				return fmt.Errorf("\ndeployment failed: %s", string(data))
			}

			var out struct {
				Services map[string][]string `json:"services"`
			}
			if err := json.Unmarshal(data, &out); err != nil {
				return err
			}

			fmt.Println("\nDeployment successful!")
			if len(out.Services) > 0 {
				fmt.Println("\nExposed services:")
				for name, urls := range out.Services {
					fmt.Printf("  %s:\n", name)
					if len(urls) == 0 {
						fmt.Println("    (no urls)")
					}
					for _, u := range urls {
						fmt.Printf("    - %s\n", u)
					}
				}
			}
			return nil
		},
	}
	deployCmd.Flags().StringP("host", "H", "", "Nimbus server host (default $NIMBUS_HOST)")
	deployCmd.Flags().StringP("file", "f", "./nimbus.yaml", "Path to deployment file")
	deployCmd.Flags().StringP("apikey", "a", "", "API key (default $NIMBUS_API_KEY)")

	var projectCmd = &cobra.Command{Use: "project", Short: "Manage projects"}
	var projectCreateCmd = &cobra.Command{
		Use:   "create",
		Short: "Create a new project",
		RunE: func(cmd *cobra.Command, args []string) error {
			host := getHost(cmd)
			apiKey := getAPIKey(cmd)
			fmt.Print("Project name: ")
			var name string
			fmt.Scanln(&name)
			body, _ := json.Marshal(map[string]string{"name": name})
			req, _ := http.NewRequest("POST", host+"/projects", bytes.NewBuffer(body))
			req.Header.Set("Content-Type", "application/json")
			if apiKey != "" {
				req.Header.Set("X-API-Key", apiKey)
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				return err
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusCreated {
				data, _ := io.ReadAll(resp.Body)
				return fmt.Errorf("failed: %s", string(data))
			}
			fmt.Println("Project created!")
			return nil
		},
	}

	var projectListCmd = &cobra.Command{
		Use:   "list",
		Short: "List your projects",
		RunE: func(cmd *cobra.Command, args []string) error {
			host := getHost(cmd)
			apiKey := getAPIKey(cmd)
			req, _ := http.NewRequest("GET", host+"/projects", nil)
			if apiKey != "" {
				req.Header.Set("X-API-Key", apiKey)
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				return err
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				data, _ := io.ReadAll(resp.Body)
				return fmt.Errorf("failed: %s", string(data))
			}
			var out struct{ Projects []struct{ Name string } }
			if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
				return err
			}
			fmt.Println("Projects:")
			for _, p := range out.Projects {
				fmt.Printf("- %s\n", p.Name)
			}
			return nil
		},
	}
	var projectDeleteCmd = &cobra.Command{
		Use:   "delete [name]",
		Short: "Delete a project and all branches",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			host := getHost(cmd)
			apiKey := getAPIKey(cmd)
			url := fmt.Sprintf("%s/projects/%s", host, args[0])
			req, _ := http.NewRequest("DELETE", url, nil)
			if apiKey != "" {
				req.Header.Set("X-API-Key", apiKey)
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				return err
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				data, _ := io.ReadAll(resp.Body)
				return fmt.Errorf("failed: %s", string(data))
			}
			fmt.Println("Project deleted!")
			return nil
		},
	}
	projectCmd.AddCommand(projectCreateCmd, projectListCmd, projectDeleteCmd)
	projectCreateCmd.Flags().StringP("host", "H", "", "Nimbus host")
	projectCreateCmd.Flags().StringP("apikey", "a", "", "API key")
	projectListCmd.Flags().StringP("host", "H", "", "Nimbus host")
	projectListCmd.Flags().StringP("apikey", "a", "", "API key")
	projectDeleteCmd.Flags().StringP("host", "H", "", "Nimbus host")
	projectDeleteCmd.Flags().StringP("apikey", "a", "", "API key")

	var serviceCmd = &cobra.Command{Use: "service", Short: "Manage services"}
	var serviceListCmd = &cobra.Command{
		Use:   "list",
		Short: "List all services",
		RunE: func(cmd *cobra.Command, args []string) error {
			host := getHost(cmd)
			apiKey := getAPIKey(cmd)
			req, _ := http.NewRequest("GET", host+"/services", nil)
			if apiKey != "" {
				req.Header.Set("X-API-Key", apiKey)
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				return err
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				data, _ := io.ReadAll(resp.Body)
				return fmt.Errorf("failed: %s", string(data))
			}
			var out struct {
				Services []struct {
					ProjectName   string `json:"project"`
					ProjectBranch string `json:"branch"`
					ServiceName   string `json:"name"`
					Status        string `json:"status"`
				}
			}
			if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
				return err
			}
			tree := make(map[string]map[string][]struct{ Name, Status string })
			for _, s := range out.Services {
				if tree[s.ProjectName] == nil {
					tree[s.ProjectName] = make(map[string][]struct{ Name, Status string })
				}
				tree[s.ProjectName][s.ProjectBranch] = append(tree[s.ProjectName][s.ProjectBranch], struct{ Name, Status string }{s.ServiceName, s.Status})
			}
			projects := make([]string, 0, len(tree))
			for p := range tree {
				projects = append(projects, p)
			}
			sort.Strings(projects)
			for _, project := range projects {
				fmt.Printf("Project: %s\n", project)
				branchesMap := tree[project]
				branches := make([]string, 0, len(branchesMap))
				for b := range branchesMap {
					branches = append(branches, b)
				}
				sort.SliceStable(branches, func(i, j int) bool {
					a, b := branches[i], branches[j]
					isMainA := a == "main" || a == "master"
					isMainB := b == "main" || b == "master"
					if isMainA && !isMainB {
						return true
					}
					if !isMainA && isMainB {
						return false
					}
					return a < b
				})
				for _, branch := range branches {
					fmt.Printf("  Branch: %s\n", branch)
					services := branchesMap[branch]
					sort.Slice(services, func(i, j int) bool { return services[i].Name < services[j].Name })
					for _, svc := range services {
						fmt.Printf("    - Service: %s (Status: %s)\n", svc.Name, svc.Status)
					}
				}
			}
			return nil
		},
	}

	var serviceGetCmd = &cobra.Command{
		Use:   "get [name]",
		Short: "Get service details",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			host := getHost(cmd)
			apiKey := getAPIKey(cmd)
			project, _ := cmd.Flags().GetString("project")
			branch, _ := cmd.Flags().GetString("branch")
			if branch == "" {
				branch = "main"
			}
			url := fmt.Sprintf("%s/services/%s?project=%s&branch=%s", host, args[0], project, branch)
			req, _ := http.NewRequest("GET", url, nil)
			if apiKey != "" {
				req.Header.Set("X-API-Key", apiKey)
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				return err
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				data, _ := io.ReadAll(resp.Body)
				return fmt.Errorf("failed: %s", string(data))
			}
			var out struct {
				Project   string
				Branch    string
				Name      string
				NodePorts []int32                        `json:"nodePorts"`
				Ingress   *string                        `json:"ingress"`
				Pods      []struct{ Name, Phase string } `json:"pods"`
				Logs      string                         `json:"logs"`
			}
			if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
				return err
			}
			fmt.Printf("Service %s (%s/%s)\n", out.Name, out.Project, out.Branch)
			if out.Ingress != nil {
				fmt.Printf("  Ingress: %s\n", *out.Ingress)
			}
			if len(out.NodePorts) > 0 {
				host := os.Getenv("NIMBUS_HOST")
				baseDomain := ""

				if host != "" {
					parsed, err := urllib.Parse(host)
					if err == nil {
						// parsed.Host gives you "nimbus.prayujt.com"
						parts := strings.Split(parsed.Hostname(), ".")
						n := len(parts)
						if n >= 2 {
							baseDomain = strings.Join(parts[n-2:], ".")
						}
					}
				}

				ports := make([]string, len(out.NodePorts))
				for i, p := range out.NodePorts {
					if baseDomain != "" {
						ports[i] = fmt.Sprintf("%s:%d", baseDomain, p)
					} else {
						ports[i] = fmt.Sprintf("%d", p)
					}
				}
				fmt.Printf("  NodePorts: [%s]\n", strings.Join(ports, ", "))
			}
			fmt.Println("  Pods:")
			for _, p := range out.Pods {
				fmt.Printf("    %s - %s\n", p.Name, p.Phase)
			}
			if out.Logs != "" {
				fmt.Println("  Last Logs:")
				lines := strings.Split(strings.TrimSpace(out.Logs), "\n")
				for _, l := range lines {
					fmt.Printf("    %s\n", l)
				}
			}
			return nil
		},
	}
	serviceGetCmd.Flags().String("project", "", "Project name")
	serviceGetCmd.Flags().String("branch", "", "Branch name")

	var serviceLogsCmd = &cobra.Command{
		Use:   "logs [name]",
		Short: "Stream logs from a service",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			host := getHost(cmd)
			apiKey := getAPIKey(cmd)
			project, _ := cmd.Flags().GetString("project")
			branch, _ := cmd.Flags().GetString("branch")
			if branch == "" {
				branch = "main"
			}
			url := fmt.Sprintf("%s/services/%s/logs?project=%s&branch=%s", host, args[0], project, branch)
			req, _ := http.NewRequest("GET", url, nil)
			if apiKey != "" {
				req.Header.Set("X-API-Key", apiKey)
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				return err
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				data, _ := io.ReadAll(resp.Body)
				return fmt.Errorf("failed: %s", string(data))
			}
			_, err = io.Copy(os.Stdout, resp.Body)
			return err
		},
	}
	serviceLogsCmd.Flags().String("project", "", "Project name")
	serviceLogsCmd.Flags().String("branch", "", "Branch name")
	serviceLogsCmd.Flags().StringP("host", "H", "", "Nimbus host")
	serviceLogsCmd.Flags().StringP("apikey", "a", "", "API key")

	serviceCmd.AddCommand(serviceListCmd, serviceGetCmd, serviceLogsCmd)
	serviceListCmd.Flags().StringP("host", "H", "", "Nimbus host")
	serviceListCmd.Flags().StringP("apikey", "a", "", "API key")
	serviceGetCmd.Flags().StringP("host", "H", "", "Nimbus host")
	serviceGetCmd.Flags().StringP("apikey", "a", "", "API key")

	var branchCmd = &cobra.Command{Use: "branch", Short: "Manage branches"}
	var branchDeleteCmd = &cobra.Command{
		Use:   "delete",
		Short: "Delete a branch",
		RunE: func(cmd *cobra.Command, args []string) error {
			host := getHost(cmd)
			apiKey := getAPIKey(cmd)
			project, _ := cmd.Flags().GetString("project")
			branch, _ := cmd.Flags().GetString("branch")
			if project == "" || branch == "" {
				return fmt.Errorf("project and branch are required")
			}
			url := fmt.Sprintf("%s/branch?project=%s&branch=%s", host, project, branch)
			req, _ := http.NewRequest("DELETE", url, nil)
			if apiKey != "" {
				req.Header.Set("X-API-Key", apiKey)
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				return err
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				data, _ := io.ReadAll(resp.Body)
				return fmt.Errorf("failed: %s", string(data))
			}
			fmt.Println("Branch deleted!")
			return nil
		},
	}
	branchDeleteCmd.Flags().String("project", "", "Project name")
	branchDeleteCmd.Flags().String("branch", "", "Branch name")
	branchDeleteCmd.Flags().StringP("host", "H", "", "Nimbus host")
	branchDeleteCmd.Flags().StringP("apikey", "a", "", "API key")
	branchCmd.AddCommand(branchDeleteCmd)

	rootCmd.AddCommand(serverCmd, deployCmd, projectCmd, serviceCmd, branchCmd)
	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}

func getHost(cmd *cobra.Command) string {
	host, _ := cmd.Flags().GetString("host")
	if host == "" {
		host = os.Getenv("NIMBUS_HOST")
	}
	return host
}

func getAPIKey(cmd *cobra.Command) string {
	apiKey, _ := cmd.Flags().GetString("apikey")
	if apiKey == "" {
		apiKey = os.Getenv("NIMBUS_API_KEY")
	}
	return apiKey
}
