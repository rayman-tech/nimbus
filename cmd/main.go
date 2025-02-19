package main

import (
	"fmt"
	"log"
	"net/http"

	"github.com/spf13/cobra"
)

func startServer(port string) {
	fmt.Println("Starting server on port", port)
	log.Fatal(http.ListenAndServe(":"+port, nil))
}

func main() {
	var rootCmd = &cobra.Command{Use: "nimbus"}

	var serverCmd = &cobra.Command{
		Use:   "server",
		Short: "Start the server",
		Run: func(cmd *cobra.Command, args []string) {
			port, _ := cmd.Flags().GetString("port")
			startServer(port)
		},
	}
	serverCmd.Flags().StringP("port", "p", "8080", "Port to run the server on")

	var clientCmd = &cobra.Command{
		Use:   "client",
		Short: "Run the client",
		Run: func(cmd *cobra.Command, args []string) {
			host, _ := cmd.Flags().GetString("host")
			fmt.Println("Client connecting to", host)
		},
	}
	clientCmd.Flags().StringP("host", "H", "http://localhost:8080", "URL of the host server")

	rootCmd.AddCommand(serverCmd, clientCmd)
	rootCmd.Execute()
}
