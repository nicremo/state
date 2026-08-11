package statectl

import (
	"context"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestProxyServerMirrorsRemoteToolsAndCalls(t *testing.T) {
	t.Parallel()

	remoteServer := mcp.NewServer(&mcp.Implementation{Name: "remote-state", Version: "1.0.0"}, &mcp.ServerOptions{
		Instructions: "Remote instructions",
	})
	type echoInput struct {
		Text string `json:"text"`
	}
	mcp.AddTool(remoteServer, &mcp.Tool{Name: "echo", Description: "Echo text"}, func(_ context.Context, _ *mcp.CallToolRequest, input echoInput) (*mcp.CallToolResult, any, error) {
		return nil, map[string]any{"text": input.Text}, nil
	})
	remoteClientTransport, remoteServerTransport := mcp.NewInMemoryTransports()
	go func() { _ = remoteServer.Run(context.Background(), remoteServerTransport) }()
	remoteClient := mcp.NewClient(&mcp.Implementation{Name: "statectl", Version: "1.0.0"}, nil)
	remoteSession, err := remoteClient.Connect(context.Background(), remoteClientTransport, nil)
	if err != nil {
		t.Fatalf("remote Connect() error = %v", err)
	}
	t.Cleanup(func() { _ = remoteSession.Close() })

	proxy, err := NewProxyServer(context.Background(), remoteSession, "test-version")
	if err != nil {
		t.Fatalf("NewProxyServer() error = %v", err)
	}
	localClientTransport, localServerTransport := mcp.NewInMemoryTransports()
	go func() { _ = proxy.Run(context.Background(), localServerTransport) }()
	localClient := mcp.NewClient(&mcp.Implementation{Name: "harness", Version: "1.0.0"}, nil)
	localSession, err := localClient.Connect(context.Background(), localClientTransport, nil)
	if err != nil {
		t.Fatalf("local Connect() error = %v", err)
	}
	t.Cleanup(func() { _ = localSession.Close() })
	if localSession.InitializeResult().Instructions != "Remote instructions" {
		t.Fatalf("proxy instructions = %q", localSession.InitializeResult().Instructions)
	}
	tools, err := localSession.ListTools(context.Background(), nil)
	if err != nil || len(tools.Tools) != 1 || tools.Tools[0].Name != "echo" {
		t.Fatalf("proxy tools = %#v, %v", tools, err)
	}
	result, err := localSession.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "echo",
		Arguments: map[string]any{"text": "hello"},
	})
	if err != nil || result.IsError {
		t.Fatalf("proxy CallTool() = %#v, %v", result, err)
	}
	structured, ok := result.StructuredContent.(map[string]any)
	if !ok || structured["text"] != "hello" {
		t.Fatalf("proxy structured content = %#v", result.StructuredContent)
	}
}
