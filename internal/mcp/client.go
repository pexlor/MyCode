package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
)

const protocolVersion = "2025-06-18"

type Tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

type ToolResult struct {
	Content []Content `json:"content"`
	IsError bool      `json:"isError"`
}

type Content struct {
	Type     string          `json:"type"`
	Text     string          `json:"text,omitempty"`
	Data     string          `json:"data,omitempty"`
	MIMEType string          `json:"mimeType,omitempty"`
	URI      string          `json:"uri,omitempty"`
	Resource json.RawMessage `json:"resource,omitempty"`
}

type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int64  `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *rpcError       `json:"error"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type response struct {
	result json.RawMessage
	err    error
}

// Client owns one stdio MCP server process. Calls are safe for concurrent use.
type Client struct {
	name    string
	command *exec.Cmd
	stdin   io.WriteCloser
	writer  *bufio.Writer

	writeMu sync.Mutex
	mu      sync.Mutex
	pending map[int64]chan response
	nextID  atomic.Int64
	closed  bool
}

func Start(ctx context.Context, name string, config ServerConfig) (*Client, error) {
	cmd := exec.Command(config.Command, config.Args...)
	cmd.Env = append(os.Environ(), environment(config.Env)...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("create stdin for MCP server %q: %w", name, err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("create stdout for MCP server %q: %w", name, err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start MCP server %q: %w", name, err)
	}
	c := &Client{name: name, command: cmd, stdin: stdin, writer: bufio.NewWriter(stdin), pending: make(map[int64]chan response)}
	go c.readResponses(stdout)
	go func() {
		err := cmd.Wait()
		c.failPending(fmt.Errorf("MCP server %q exited: %w", name, err))
	}()

	var initialized struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	if err := c.request(ctx, "initialize", map[string]any{
		"protocolVersion": protocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]string{"name": "MyCode", "version": "0.1.0"},
	}, &initialized); err != nil {
		c.Close()
		return nil, err
	}
	if initialized.ProtocolVersion != protocolVersion {
		c.Close()
		return nil, fmt.Errorf("MCP server %q negotiated unsupported protocol version %q", name, initialized.ProtocolVersion)
	}
	if err := c.notify("notifications/initialized", nil); err != nil {
		c.Close()
		return nil, err
	}
	return c, nil
}

func (c *Client) ListTools(ctx context.Context) ([]Tool, error) {
	var result struct {
		Tools []Tool `json:"tools"`
	}
	if err := c.request(ctx, "tools/list", map[string]any{}, &result); err != nil {
		return nil, err
	}
	sort.Slice(result.Tools, func(i, j int) bool { return result.Tools[i].Name < result.Tools[j].Name })
	return result.Tools, nil
}

func (c *Client) CallTool(ctx context.Context, name string, arguments map[string]any) (ToolResult, error) {
	var result ToolResult
	err := c.request(ctx, "tools/call", map[string]any{"name": name, "arguments": arguments}, &result)
	return result, err
}

func (c *Client) request(ctx context.Context, method string, params any, target any) error {
	id := c.nextID.Add(1)
	ch := make(chan response, 1)
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return errors.New("MCP client is closed")
	}
	c.pending[id] = ch
	c.mu.Unlock()
	if err := c.write(rpcRequest{JSONRPC: "2.0", ID: id, Method: method, Params: params}); err != nil {
		c.removePending(id)
		return err
	}
	select {
	case received := <-ch:
		if received.err != nil {
			return received.err
		}
		if err := json.Unmarshal(received.result, target); err != nil {
			return fmt.Errorf("decode MCP %s response: %w", method, err)
		}
		return nil
	case <-ctx.Done():
		c.removePending(id)
		return ctx.Err()
	}
}

func (c *Client) notify(method string, params any) error {
	return c.write(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
}

func (c *Client) write(value any) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if err := json.NewEncoder(c.writer).Encode(value); err != nil {
		return fmt.Errorf("write MCP request: %w", err)
	}
	if err := c.writer.Flush(); err != nil {
		return fmt.Errorf("flush MCP request: %w", err)
	}
	return nil
}

func (c *Client) readResponses(reader io.Reader) {
	decoder := json.NewDecoder(bufio.NewReader(reader))
	for {
		var raw json.RawMessage
		if err := decoder.Decode(&raw); err != nil {
			if !errors.Is(err, io.EOF) {
				c.failPending(fmt.Errorf("read MCP response: %w", err))
			}
			return
		}
		if len(raw) > 0 && raw[0] == '[' {
			var batch []json.RawMessage
			if err := json.Unmarshal(raw, &batch); err != nil {
				c.failPending(fmt.Errorf("decode MCP response batch: %w", err))
				return
			}
			for _, item := range batch {
				c.handleResponse(item)
			}
			continue
		}
		c.handleResponse(raw)
	}
}

func (c *Client) handleResponse(raw json.RawMessage) {
	var message rpcResponse
	if err := json.Unmarshal(raw, &message); err != nil {
		return // A malformed unsolicited message cannot be correlated to a call.
	}
	var id int64
	if len(message.ID) == 0 || json.Unmarshal(message.ID, &id) != nil {
		return // Server notifications and requests do not complete client calls.
	}
	c.mu.Lock()
	ch := c.pending[id]
	delete(c.pending, id)
	c.mu.Unlock()
	if ch == nil {
		return
	}
	if message.Error != nil {
		ch <- response{err: fmt.Errorf("MCP error %d: %s", message.Error.Code, message.Error.Message)}
	} else {
		ch <- response{result: message.Result}
	}
}

func (c *Client) removePending(id int64) {
	c.mu.Lock()
	delete(c.pending, id)
	c.mu.Unlock()
}

func (c *Client) failPending(err error) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.closed = true
	pending := c.pending
	c.pending = make(map[int64]chan response)
	c.mu.Unlock()
	for _, ch := range pending {
		ch <- response{err: err}
	}
}

func (c *Client) Close() error {
	c.failPending(errors.New("MCP client closed"))
	_ = c.stdin.Close()
	if c.command.Process != nil {
		_ = c.command.Process.Kill()
	}
	return nil
}

func environment(values map[string]string) []string {
	result := make([]string, 0, len(values))
	for key, value := range values {
		result = append(result, key+"="+value)
	}
	return result
}

func (r ToolResult) Text() string {
	parts := make([]string, 0, len(r.Content))
	for _, content := range r.Content {
		if content.Type == "text" {
			parts = append(parts, content.Text)
			continue
		}
		encoded, err := json.Marshal(content)
		if err == nil {
			parts = append(parts, string(encoded))
		}
	}
	return strings.Join(parts, "\n")
}
