package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"nimbus/internal/api"

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
				host = "http://localhost:8080"
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
						time.Sleep(100 * time.Millisecond)
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
				return fmt.Errorf("deployment failed: %s", string(data))
			}

			var out struct {
				Services map[string][]string `json:"services"`
			}
			if err := json.Unmarshal(data, &out); err != nil {
				return err
			}

			fmt.Println("Deployment successful!")
			if len(out.Services) > 0 {
				fmt.Println("Exposed services:")
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

	rootCmd.AddCommand(serverCmd, deployCmd)
	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}
