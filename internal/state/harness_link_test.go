package state

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestValidHarnessLink(t *testing.T) {
	t.Parallel()

	valid := []string{
		"https://app.t3.codes/threads/33541916-e460-4b9b-8688-6c2046b0a733/mcp%3Alaunch%3Aabc",
		"https://example.com/x",
	}
	for _, link := range valid {
		if !ValidHarnessLink(link) {
			t.Errorf("ValidHarnessLink(%q) = false, want true", link)
		}
	}
	invalid := []string{
		"",
		"http://app.t3.codes/threads/a/b",
		"https://user:secret@app.t3.codes/threads/a/b",
		"/threads/a/b",
		"https://",
		"javascript:alert(1)",
		"https://app.t3.codes/" + strings.Repeat("a", 600),
	}
	for _, link := range invalid {
		if ValidHarnessLink(link) {
			t.Errorf("ValidHarnessLink(%q) = true, want false", link)
		}
	}
}

func TestCompleteRunStoresHarnessLinkAndSessionShowsIt(t *testing.T) {
	t.Parallel()

	fixture := newSessionFixture(t)
	ctx := context.Background()
	view := fixture.start(t, "")

	run := mustClaim(t, fixture.service, fixture.runner)
	run = mustReportStarted(t, fixture.service, fixture.runner, run)
	_, err := fixture.service.CompleteAgentRun(ctx, fixture.runner, CompleteRunInput{
		RunID: run.ID, Outcome: AgentRunStatusSucceeded, ExitCode: 0, ExpectedRevision: run.Revision,
		HarnessLink:      "http://insecure.example/x",
		MutationMetadata: MutationMetadata{ClientRequestID: requestID()},
	})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("insecure link error = %v, want ErrInvalidInput", err)
	}

	link := "https://app.t3.codes/threads/env/thread-1"
	completed, err := fixture.service.CompleteAgentRun(ctx, fixture.runner, CompleteRunInput{
		RunID: run.ID, Outcome: AgentRunStatusSucceeded, ResultText: "Fertig", HarnessSessionID: "thread-1",
		ExitCode: 0, ExpectedRevision: run.Revision, HarnessLink: link,
		MutationMetadata: MutationMetadata{ClientRequestID: requestID()},
	})
	if err != nil {
		t.Fatalf("CompleteAgentRun() error = %v", err)
	}
	if completed.HarnessLink != link {
		t.Fatalf("run link = %q", completed.HarnessLink)
	}
	got, err := fixture.service.GetAgentSession(ctx, view.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.HarnessLink != link {
		t.Fatalf("session link = %q", got.HarnessLink)
	}
}
