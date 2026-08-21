package state

import "regexp"

// projectSlugPattern keeps project and policy names URL- and file-safe. Two to
// sixty-four characters, lower case letters, digits and inner hyphens. It is the
// harness label pattern with a wider length budget.
var projectSlugPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}[a-z0-9]$`)

// ValidProjectSlug reports whether a name may identify a project or a policy.
func ValidProjectSlug(slug string) bool {
	return projectSlugPattern.MatchString(slug)
}
