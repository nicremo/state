package state

import "regexp"

// harnessPattern keeps harness labels unambiguous so that a typo cannot create
// a second actor for the same agent. Two to thirty-two characters, lower case
// letters, digits and inner hyphens.
var harnessPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,30}[a-z0-9]$`)

// knownHarnesses are the harnesses statectl configures without manual work.
// Any other valid label pairs normally and receives printed MCP instructions.
var knownHarnesses = []string{"codex", "claude-code", "opencode"}

// ValidHarness reports whether a label may identify a paired agent.
func ValidHarness(harness string) bool {
	return harnessPattern.MatchString(harness)
}

// KnownHarnesses lists the harnesses with a shipped statectl integration.
func KnownHarnesses() []string {
	return append([]string(nil), knownHarnesses...)
}

// KnownHarness reports whether statectl can write this harness configuration.
func KnownHarness(harness string) bool {
	for _, candidate := range knownHarnesses {
		if candidate == harness {
			return true
		}
	}
	return false
}
