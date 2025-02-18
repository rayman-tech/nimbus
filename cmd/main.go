package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
)

func startServer(port string) {
	fmt.Println("Starting server on port", port)
	log.Fatal(http.ListenAndServe(":"+port, nil))
}

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage: nimbus [server|client] [flags]")
		os.Exit(1)
	}

	mode := os.Args[1]

	switch mode {
	case "server":
		serverFlags := flag.NewFlagSet("server", flag.ExitOnError)
		port := serverFlags.String("port", "8080", "Port to run the server on")
		serverFlags.Parse(os.Args[2:])
		startServer(*port)

	case "client":
		clientFlags := flag.NewFlagSet("client", flag.ExitOnError)
		_ = clientFlags.String("host", "http://localhost:8080", "URL of the host server")
		clientFlags.Parse(os.Args[2:])

	default:
		fmt.Println("Invalid mode. Use 'server' or 'client'")
		os.Exit(1)
	}
}
