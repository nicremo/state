package state

import (
	"regexp"
	"strings"
	"unicode"
)

// A voice note's body is a short list of the points the owner made, never
// the transcript itself: the transcript stays next to the recording. The
// notes agent is told so, and VoiceDocumentProblem checks its answer, so a
// model that copies the transcript is asked once more instead of filling the
// note with it.

const (
	voiceShingleWords   = 5
	maxVoiceCopiedShare = 0.5
)

var (
	voiceListItem = regexp.MustCompile(`^([-*+]|\d+[.)])\s+\S`)
	voiceRule     = regexp.MustCompile(`^(-{3,}|\*{3,}|_{3,})$`)
	voiceMarker   = regexp.MustCompile(`^([-*+]|\d+[.)])\s+(\[[ xX]\]\s+)?`)
)

var placeholderHeadings = map[string]bool{
	"sprachnotiz": true, "sprachnachricht": true, "sprachmemo": true, "aufnahme": true,
	"transkript": true, "notiz": true, "voice note": true, "voice memo": true,
	"transcript": true, "recording": true, "note": true,
}

// VoiceDocumentProblem says what is wrong with a voice note's body, or
// returns an empty string when it is a list in the model's own words.
func VoiceDocumentProblem(document string, transcript string) string {
	if strings.TrimSpace(transcript) == "" {
		return ""
	}
	// Dash bullets are turned into list markers before the note is stored,
	// so they count as a list here too.
	document = RemoveDashes(document, false)
	listItems := 0
	for _, line := range strings.Split(document, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case line == "" || voiceRule.MatchString(line) || strings.HasPrefix(line, "|"):
		case strings.HasPrefix(line, "#"):
			heading := strings.ToLower(strings.TrimSpace(strings.Trim(strings.TrimLeft(line, "#"), " :")))
			if placeholderHeadings[heading] {
				return "it has a placeholder heading (" + heading + ") instead of content"
			}
		case voiceListItem.MatchString(line):
			listItems++
		default:
			return "it contains running text instead of bullet points"
		}
	}
	if listItems == 0 {
		return "it has no bullet points"
	}
	if copiedShare(document, transcript) > maxVoiceCopiedShare {
		return "it repeats the transcript instead of condensing it"
	}
	return ""
}

// copiedShare is the part of the body's five-word sequences that appear in
// the transcript word for word.
func copiedShare(document string, transcript string) float64 {
	spoken := voiceWords(transcript)
	if len(spoken) < voiceShingleWords {
		return 0
	}
	known := map[string]bool{}
	for index := 0; index+voiceShingleWords <= len(spoken); index++ {
		known[strings.Join(spoken[index:index+voiceShingleWords], " ")] = true
	}
	written := make([]string, 0)
	for _, line := range strings.Split(document, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "#") || strings.HasPrefix(line, "|") {
			continue
		}
		written = append(written, voiceWords(voiceMarker.ReplaceAllString(line, ""))...)
	}
	total := len(written) - voiceShingleWords + 1
	if total <= 0 {
		return 0
	}
	copied := 0
	for index := 0; index < total; index++ {
		if known[strings.Join(written[index:index+voiceShingleWords], " ")] {
			copied++
		}
	}
	return float64(copied) / float64(total)
}

func voiceWords(text string) []string {
	return strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
}
