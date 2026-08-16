package mcpserver

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	stateauth "github.com/nicremo/state/internal/auth"
	"github.com/nicremo/state/internal/state"
)

// The product promise is that every agent the owner uses stays connected at the
// same time. This drives three of them through one server concurrently, one of
// which has no shipped statectl integration, and checks that the audit chain
// keeps them apart.
func TestMCPServerServesSeveralAgentsConcurrently(t *testing.T) {
	t.Parallel()

	agents := []struct {
		harness     string
		displayName string
		deviceName  string
		title       string
		requestID   string
	}{
		{"codex", "Codex", "Mac mini", "Review the relay dry run", "01989f10-0000-7000-8000-000000000001"},
		{"claude-code", "Claude Code", "MacBook Pro", "Draft the release notes", "01989f10-0000-7000-8000-000000000002"},
		{"pi", "Pi", "Workstation", "Check the backup restore", "01989f10-0000-7000-8000-000000000003"},
	}

	fixture := newTestMCPFixture(t)
	mux := http.NewServeMux()
	mux.Handle("/mcp", fixture.handler)
	httpServer := httptest.NewServer(mux)
	t.Cleanup(httpServer.Close)

	tokens := make([]string, len(agents))
	for index, agent := range agents {
		tokens[index] = fixture.pairHarness(t, agent.harness, agent.displayName, agent.deviceName)
	}
	for index := range tokens {
		for other := index + 1; other < len(tokens); other++ {
			if tokens[index] == tokens[other] {
				t.Fatal("paired agents must not share a credential")
			}
		}
	}

	// Every agent holds its own session at the same time before any of them writes.
	sessions := make([]*mcp.ClientSession, len(agents))
	for index, agent := range agents {
		client := mcp.NewClient(&mcp.Implementation{Name: "state-test-" + agent.harness, Version: "1.0.0"}, nil)
		session, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{
			Endpoint:             httpServer.URL + "/mcp",
			HTTPClient:           &http.Client{Transport: bearerTransport{token: tokens[index]}},
			DisableStandaloneSSE: true,
		}, nil)
		if err != nil {
			t.Fatalf("Connect(%s) error = %v", agent.harness, err)
		}
		t.Cleanup(func() { _ = session.Close() })
		sessions[index] = session
	}

	reminderIDs := make([]string, len(agents))
	errs := make([]error, len(agents))
	var group sync.WaitGroup
	for index, agent := range agents {
		group.Add(1)
		go func() {
			defer group.Done()
			result, err := sessions[index].CallTool(context.Background(), &mcp.CallToolParams{
				Name: "create_reminder",
				Arguments: map[string]any{
					"title":             agent.title,
					"client_request_id": agent.requestID,
					"source_text":       "Remind me to " + agent.title,
				},
			})
			if err != nil {
				errs[index] = err
				return
			}
			if result.IsError {
				errs[index] = fmt.Errorf("%s create_reminder tool error: %s", agent.harness, toolErrorText(result))
				return
			}
			structured, ok := result.StructuredContent.(map[string]any)
			if !ok {
				errs[index] = fmt.Errorf("%s structured content type = %T", agent.harness, result.StructuredContent)
				return
			}
			reminder, ok := structured["reminder"].(map[string]any)
			if !ok {
				errs[index] = fmt.Errorf("%s structured reminder = %#v", agent.harness, structured["reminder"])
				return
			}
			id, _ := reminder["id"].(string)
			if id == "" {
				errs[index] = fmt.Errorf("%s created reminder without an ID", agent.harness)
				return
			}
			reminderIDs[index] = id
		}()
	}
	group.Wait()
	for index, err := range errs {
		if err != nil {
			t.Fatalf("concurrent create by %s failed: %v", agents[index].harness, err)
		}
	}

	// Each write must be attributed to its own harness actor.
	actorIDs := map[string]string{}
	for index, agent := range agents {
		events, err := fixture.state.ListAuditEvents(context.Background(), reminderIDs[index])
		if err != nil {
			t.Fatalf("ListAuditEvents(%s) error = %v", agent.harness, err)
		}
		if len(events) != 1 {
			t.Fatalf("%s audit events = %d, want 1", agent.harness, len(events))
		}
		actor := events[0].Actor
		if actor.Kind != state.ActorKindHarness || actor.Harness != agent.harness {
			t.Fatalf("%s audit actor = %#v", agent.harness, actor)
		}
		if actor.DisplayName != agent.displayName || actor.DeviceName != agent.deviceName {
			t.Fatalf("%s audit actor identity = %#v", agent.harness, actor)
		}
		if previous, seen := actorIDs[actor.ID]; seen {
			t.Fatalf("%s reuses the actor of %s", agent.harness, previous)
		}
		actorIDs[actor.ID] = agent.harness
	}
	if len(actorIDs) != len(agents) {
		t.Fatalf("distinct actors = %d, want %d", len(actorIDs), len(agents))
	}

	// A revoked agent stops immediately while the others keep working.
	if err := fixture.auth.RevokeActor(context.Background(), fixture.owner, actorIDForHarness(t, actorIDs, "pi")); err != nil {
		t.Fatalf("RevokeActor(pi) error = %v", err)
	}
	if _, err := sessions[2].CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "get_reminder",
		Arguments: map[string]any{"reminder_id": reminderIDs[2]},
	}); err == nil {
		t.Fatal("revoked agent still reached the MCP endpoint")
	}
	result, err := sessions[0].CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "get_reminder",
		Arguments: map[string]any{"reminder_id": reminderIDs[0]},
	})
	if err != nil || result.IsError {
		t.Fatalf("revoking one agent broke another: %#v, %v", result, err)
	}
}

func TestPairingRejectsAmbiguousHarnessLabels(t *testing.T) {
	t.Parallel()

	fixture := newTestMCPFixture(t)
	for _, harness := range []string{"", "Claude Code", "claude code", "codex_cli", "-codex"} {
		_, err := fixture.auth.CreatePairingCode(context.Background(), fixture.owner, stateauth.PairingCodeRequest{
			Harness:     harness,
			DisplayName: "Some agent",
			DeviceName:  "Mac",
		})
		if err == nil {
			t.Fatalf("CreatePairingCode(%q) succeeded, want rejection", harness)
		}
	}
}

func toolErrorText(result *mcp.CallToolResult) string {
	messages := make([]string, 0, len(result.Content))
	for _, content := range result.Content {
		if text, ok := content.(*mcp.TextContent); ok {
			messages = append(messages, text.Text)
		}
	}
	if len(messages) == 0 {
		return fmt.Sprintf("%#v", result.Content)
	}
	return strings.Join(messages, "; ")
}

func actorIDForHarness(t *testing.T, actorIDs map[string]string, harness string) string {
	t.Helper()

	for id, candidate := range actorIDs {
		if candidate == harness {
			return id
		}
	}
	t.Fatalf("no actor for harness %q", harness)
	return ""
}
