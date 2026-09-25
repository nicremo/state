package state

import (
	"regexp"
	"strings"
)

// The owner does not want dashes (U+2013, U+2014) in anything the notes AI
// writes. The model is told so, and every text it returns passes through
// RemoveDashes before it is stored, so a model that ignores the rule still
// cannot put one into a note.

var (
	dashListLine     = regexp.MustCompile(`(?m)^([ \t]*)[\x{2013}\x{2014}][ \t]+`)
	dashNumberRange  = regexp.MustCompile(`(\d)[ \t]*[\x{2013}\x{2014}][ \t]*(\d)`)
	dashBeforeEnd    = regexp.MustCompile(`(?m)[ \t]*[\x{2013}\x{2014}]+[ \t]*([.,;:!?)]|$)`)
	dashLineStart    = regexp.MustCompile(`(?m)^([ \t]*)[\x{2013}\x{2014}]+[ \t]*`)
	dashBetweenWords = regexp.MustCompile(`[ \t]*[,;:]?[ \t]*[\x{2013}\x{2014}]+[ \t]*`)
)

// RemoveDashes replaces every dash by what fits its place: a hyphen in a
// number range, a list marker at the start of a line, a colon for the first
// break in a title and a comma everywhere else. A dash before punctuation or
// at the end of a line is dropped.
func RemoveDashes(text string, title bool) string {
	if !strings.ContainsAny(text, "\xe2\x80\x93\xe2\x80\x94") {
		return text
	}
	text = dashListLine.ReplaceAllString(text, "${1}- ")
	text = dashNumberRange.ReplaceAllString(text, "${1}-${2}")
	text = dashBeforeEnd.ReplaceAllString(text, "${1}")
	// A dash glued to the start of a line is a list marker too.
	text = dashLineStart.ReplaceAllString(text, "${1}- ")
	colonUsed := !title || strings.Contains(text, ":")
	return dashBetweenWords.ReplaceAllStringFunc(text, func(string) string {
		if !colonUsed {
			colonUsed = true
			return ": "
		}
		return ", "
	})
}
