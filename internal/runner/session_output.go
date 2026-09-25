package runner

import (
	"bufio"
	"bytes"
	"encoding/json"
	"strings"

	"github.com/nicremo/state/internal/state"
)

// agentOutputLimit bounds the standard output the runner keeps to read the
// agent's final message. It stays on the workstation; only the redacted
// final message is reported.
const agentOutputLimit = 4 << 20

// parsedOutput is what the runner learns from an agent CLI's structured
// output: its final message, its own session ID for the next round, and
// whether the CLI reported the round as failed.
type parsedOutput struct {
	Text      string
	SessionID string
	Failed    bool
}

// parseAgentOutput reads the output format each CLI was started with. An
// adapter without a structured format reports its plain output.
func parseAgentOutput(adapter string, stdout []byte) parsedOutput {
	var parsed parsedOutput
	switch adapter {
	case "claude-code":
		parsed = parseClaudeOutput(stdout)
	case "codex":
		parsed = parseCodexOutput(stdout)
	case "kimi-code":
		parsed = parseKimiOutput(stdout)
	default:
		parsed = parsedOutput{Text: strings.TrimSpace(string(stdout))}
	}
	if !state.ValidHarnessSessionID(parsed.SessionID) {
		parsed.SessionID = ""
	}
	return parsed
}

// parseClaudeOutput reads `claude -p --output-format json`: one result
// object, possibly after other lines.
func parseClaudeOutput(stdout []byte) parsedOutput {
	var parsed parsedOutput
	forEachJSONLine(stdout, func(line []byte) {
		var result struct {
			Type      string `json:"type"`
			IsError   bool   `json:"is_error"`
			Result    string `json:"result"`
			SessionID string `json:"session_id"`
		}
		if json.Unmarshal(line, &result) != nil || result.Type != "result" {
			return
		}
		parsed = parsedOutput{Text: strings.TrimSpace(result.Result), SessionID: result.SessionID, Failed: result.IsError}
	})
	return parsed
}

// parseCodexOutput reads `codex exec --json`: the thread ID from
// thread.started and the last agent message.
func parseCodexOutput(stdout []byte) parsedOutput {
	var parsed parsedOutput
	forEachJSONLine(stdout, func(line []byte) {
		var event struct {
			Type     string `json:"type"`
			ThreadID string `json:"thread_id"`
			Item     struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"item"`
		}
		if json.Unmarshal(line, &event) != nil {
			return
		}
		switch {
		case event.Type == "thread.started" && event.ThreadID != "":
			parsed.SessionID = event.ThreadID
		case event.Type == "item.completed" && event.Item.Type == "agent_message":
			parsed.Text = strings.TrimSpace(event.Item.Text)
		case event.Type == "turn.failed":
			parsed.Failed = true
		}
	})
	return parsed
}

// parseKimiOutput reads `kimi -p --output-format stream-json`: the last
// assistant message and the session named by the resume hint.
func parseKimiOutput(stdout []byte) parsedOutput {
	var parsed parsedOutput
	forEachJSONLine(stdout, func(line []byte) {
		var message struct {
			Role      string `json:"role"`
			Type      string `json:"type"`
			Content   any    `json:"content"`
			SessionID string `json:"session_id"`
		}
		if json.Unmarshal(line, &message) != nil {
			return
		}
		switch {
		case message.Role == "assistant":
			if text, ok := message.Content.(string); ok && strings.TrimSpace(text) != "" {
				parsed.Text = strings.TrimSpace(text)
			}
		case message.Type == "session.resume_hint" && message.SessionID != "":
			parsed.SessionID = message.SessionID
		}
	})
	return parsed
}

func forEachJSONLine(stdout []byte, visit func([]byte)) {
	scanner := bufio.NewScanner(bytes.NewReader(stdout))
	scanner.Buffer(make([]byte, 0, 64<<10), agentOutputLimit)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) > 0 && line[0] == '{' {
			visit(line)
		}
	}
}
