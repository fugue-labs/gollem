// Package mcp provides a Model Context Protocol (MCP) client that discovers
// and invokes tools from MCP servers. It supports the stdio transport using
// JSON-RPC 2.0, enabling agents to use tools from any MCP-compatible server.
//
// # Usage
//
//	client, err := mcp.NewStdioClient(ctx, "npx", "-y", "@modelcontextprotocol/server-filesystem", "/tmp")
//	defer client.Close()
//
//	tools, err := client.Tools(ctx)
//	agent := core.NewAgent[string](model, core.WithTools[string](tools...))
package mcp
