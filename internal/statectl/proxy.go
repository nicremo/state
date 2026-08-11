package statectl

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func NewProxyServer(ctx context.Context, remote *mcp.ClientSession, version string) (*mcp.Server, error) {
	toolsResult, err := remote.ListTools(ctx, nil)
	if err != nil {
		return nil, err
	}
	instructions := ""
	if initialized := remote.InitializeResult(); initialized != nil {
		instructions = initialized.Instructions
	}
	proxy := mcp.NewServer(&mcp.Implementation{Name: "statectl", Version: version}, &mcp.ServerOptions{
		Instructions: instructions,
	})
	for _, remoteTool := range toolsResult.Tools {
		tool := *remoteTool
		name := tool.Name
		proxy.AddTool(&tool, func(ctx context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return remote.CallTool(ctx, &mcp.CallToolParams{
				Name:      name,
				Arguments: request.Params.Arguments,
			})
		})
	}
	return proxy, nil
}
