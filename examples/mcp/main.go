// Example mcp demonstrates connecting to an MCP server and using its tools
// with a gollem agent. The MCP tools are automatically discovered and
// converted to gollem tools.
package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/fugue-labs/gollem/core"
	"github.com/fugue-labs/gollem/provider/anthropic"
	mcpclient "github.com/fugue-labs/gollem/ext/mcp"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage: mcp <mcp-server-command> [args...]")
		fmt.Println("Example: mcp npx -y @modelcontextprotocol/server-filesystem /tmp")
		os.Exit(1)
	}

	ctx := context.Background()

	// Connect to the MCP server.
	command := os.Args[1]
	args := os.Args[2:]
	client, err := mcpclient.NewStdioClient(ctx, command, args...)
	if err != nil {
		log.Fatalf("Failed to connect to MCP server: %v", err)
	}
	defer client.Close()

	// Discover tools from the MCP server.
	tools, err := client.Tools(ctx)
	if err != nil {
		log.Fatalf("Failed to discover tools: %v", err)
	}

	fmt.Printf("Discovered %d tools from MCP server:\n", len(tools))
	for _, t := range tools {
		fmt.Printf("  - %s: %s\n", t.Definition.Name, t.Definition.Description)
	}
	fmt.Println()

	// Create an agent with the MCP tools.
	model := anthropic.New()
	agent := core.NewAgent[string](model,
		core.WithSystemPrompt[string]("You are a helpful assistant with access to external tools. Use them when appropriate."),
		core.WithTools[string](tools...),
	)

	result, err := agent.Run(ctx, "What files are available?")
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println("Agent response:")
	fmt.Println(result.Output)
}
