package statectl

import (
	"context"
	"net/http"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ConnectRemote opens an MCP client session against the profile's server using
// the profile credential. Callers own the returned session and must close it.
func ConnectRemote(ctx context.Context, profile Profile, token string, clientVersion string) (*mcp.ClientSession, error) {
	if clientVersion == "" {
		clientVersion = "dev"
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "statectl", Version: clientVersion}, nil)
	return client.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint:             strings.TrimRight(profile.ServerURL, "/") + "/mcp",
		HTTPClient:           &http.Client{Transport: bearerRoundTripper{token: token}},
		DisableStandaloneSSE: true,
	}, nil)
}

type bearerRoundTripper struct {
	token string
}

func (transport bearerRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	clone := request.Clone(request.Context())
	clone.Header = request.Header.Clone()
	clone.Header.Set("Authorization", "Bearer "+transport.token)
	return http.DefaultTransport.RoundTrip(clone)
}
