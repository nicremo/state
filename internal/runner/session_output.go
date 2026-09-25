package runner

import (
	"bytes"
	"encoding/json"
	"strings"
	"sync"

	"github.com/nicremo/state/internal/state"
)

// agentOutputLimit bounds one line of an agent CLI's standard output and the
// plain output kept for adapters without a structured format. The output
// stays on the workstation; only the redacted final message is reported.
const agentOutputLimit = 4 << 20

// parsedOutput is what the runner learns from an agent CLI's structured
// output: its final message, its own session ID for the next round, and
// whether the CLI reported the round as failed.
type parsedOutput struct {
	Text      string
	SessionID string
	Failed    bool
}

// outputCollector reads an agent CLI's standard output line by line while
// it runs, so a long output never pushes early events (codex's thread ID)
// out of a buffer. A line longer than agentOutputLimit is skipped; if that
// leaves no answer, the round fails instead of looking like a silent
// success.
type outputCollector struct {
	mutex    sync.Mutex
	adapter  string
	parsed   parsedOutput
	line     []byte
	overflow bool
	lost     bool
	plain    *tailBuffer
}

func newOutputCollector(adapter string) *outputCollector {
	return &outputCollector{adapter: adapter, plain: &tailBuffer{limit: agentOutputLimit}}
}

func (collector *outputCollector) structured() bool {
	switch collector.adapter {
	case "claude-code", "codex", "kimi-code":
		return true
	}
	return false
}

func (collector *outputCollector) Write(chunk []byte) (int, error) {
	collector.mutex.Lock()
	defer collector.mutex.Unlock()
	written := len(chunk)
	if !collector.structured() {
		return collector.plain.Write(chunk)
	}
	for len(chunk) > 0 {
		index := bytes.IndexByte(chunk, '\n')
		if index < 0 {
			collector.add(chunk)
			break
		}
		collector.add(chunk[:index])
		collector.endLine()
		chunk = chunk[index+1:]
	}
	return written, nil
}

func (collector *outputCollector) add(part []byte) {
	if collector.overflow {
		return
	}
	if len(collector.line)+len(part) > agentOutputLimit {
		collector.overflow = true
		collector.line = collector.line[:0]
		return
	}
	collector.line = append(collector.line, part...)
}

func (collector *outputCollector) endLine() {
	if collector.overflow {
		collector.lost = true
		collector.overflow = false
		collector.line = collector.line[:0]
		return
	}
	line := bytes.TrimSpace(collector.line)
	if len(line) > 0 && line[0] == '{' {
		collector.visit(line)
	}
	collector.line = collector.line[:0]
}

// Finish reads a last line without a newline and returns what was learned.
func (collector *outputCollector) Finish() parsedOutput {
	collector.mutex.Lock()
	defer collector.mutex.Unlock()
	if !collector.structured() {
		return parsedOutput{Text: strings.TrimSpace(collector.plain.String())}
	}
	if len(collector.line) > 0 || collector.overflow {
		collector.endLine()
	}
	parsed := collector.parsed
	if collector.lost && parsed.Text == "" {
		parsed.Failed = true
		parsed.Text = "The agent's answer was too large to read."
	}
	if !state.ValidHarnessSessionID(parsed.SessionID) {
		parsed.SessionID = ""
	}
	return parsed
}

func (collector *outputCollector) visit(line []byte) {
	switch collector.adapter {
	case "claude-code":
		// `claude -p --output-format json`: one result object.
		var result struct {
			Type      string `json:"type"`
			IsError   bool   `json:"is_error"`
			Result    string `json:"result"`
			SessionID string `json:"session_id"`
		}
		if json.Unmarshal(line, &result) == nil && result.Type == "result" {
			collector.parsed = parsedOutput{Text: strings.TrimSpace(result.Result), SessionID: result.SessionID, Failed: result.IsError}
		}
	case "codex":
		// `codex exec --json`: thread.started carries the thread ID, the
		// last agent_message is the answer.
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
			collector.parsed.SessionID = event.ThreadID
		case event.Type == "item.completed" && event.Item.Type == "agent_message":
			collector.parsed.Text = strings.TrimSpace(event.Item.Text)
		case event.Type == "turn.failed":
			collector.parsed.Failed = true
		}
	case "kimi-code":
		// `kimi -p --output-format stream-json`: the last assistant message
		// and the session named by the resume hint.
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
				collector.parsed.Text = strings.TrimSpace(text)
			}
		case message.Type == "session.resume_hint" && message.SessionID != "":
			collector.parsed.SessionID = message.SessionID
		}
	}
}

// parseAgentOutput reads a complete output at once.
func parseAgentOutput(adapter string, stdout []byte) parsedOutput {
	collector := newOutputCollector(adapter)
	_, _ = collector.Write(stdout)
	return collector.Finish()
}
