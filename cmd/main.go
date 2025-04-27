package main

import (
	"fmt"
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
