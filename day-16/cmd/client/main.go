package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"ai-challenge/day-16/internal/mcpdemo"
)

func main() {
	defaultEndpoint := os.Getenv("MCP_ENDPOINT")
	if defaultEndpoint == "" {
		defaultEndpoint = "http://127.0.0.1:8080/mcp"
	}
	endpoint := flag.String("endpoint", defaultEndpoint, "Streamable HTTP MCP endpoint")
	flag.Parse()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	result, err := mcpdemo.ConnectAndListTools(ctx, *endpoint)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println("MCP connection established")
	fmt.Printf("Server: %s %s\n", result.ServerName, result.ServerVersion)
	fmt.Printf("Protocol: %s\n", result.ProtocolVersion)
	fmt.Printf("Tools (%d):\n", len(result.Tools))
	for _, tool := range result.Tools {
		fmt.Printf("- %s: %s\n", tool.Name, tool.Description)
	}
}
