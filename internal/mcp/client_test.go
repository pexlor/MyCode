package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
)

func TestClientStdioLifecycle(t *testing.T) {
	if os.Getenv("MCP_TEST_SERVER") == "1" {
		runTestServer()
		return
	}
	client, err := Start(context.Background(), "test", ServerConfig{
		Command: os.Args[0],
		Args:    []string{"-test.run=^TestClientStdioLifecycle$"},
		Env:     map[string]string{"MCP_TEST_SERVER": "1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	tools, err := client.ListTools(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 1 || tools[0].Name != "echo" {
		t.Fatalf("tools = %#v", tools)
	}
	result, err := client.CallTool(context.Background(), "echo", map[string]any{"value": "hello"})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError || result.Text() != "hello" {
		t.Fatalf("result = %#v", result)
	}
}

func runTestServer() {
	scanner := bufio.NewScanner(os.Stdin)
	encoder := json.NewEncoder(os.Stdout)
	for scanner.Scan() {
		var request struct {
			ID     int64          `json:"id"`
			Method string         `json:"method"`
			Params map[string]any `json:"params"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &request); err != nil {
			os.Exit(2)
		}
		if request.ID == 0 { // notifications have no id
			continue
		}
		var result any
		switch request.Method {
		case "initialize":
			result = map[string]any{"protocolVersion": protocolVersion, "capabilities": map[string]any{}}
		case "tools/list":
			result = map[string]any{"tools": []any{map[string]any{"name": "echo", "description": "echoes a value", "inputSchema": map[string]any{"type": "object"}}}}
		case "tools/call":
			args, _ := request.Params["arguments"].(map[string]any)
			result = map[string]any{"content": []any{map[string]any{"type": "text", "text": fmt.Sprint(args["value"])}}}
		default:
			_ = encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "error": map[string]any{"code": -32601, "message": "unknown method"}})
			continue
		}
		_ = encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": result})
	}
	// Test helper processes must not run the normal test suite after stdin closes.
	os.Exit(0)
}
