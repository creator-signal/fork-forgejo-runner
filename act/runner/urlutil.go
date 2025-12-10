package runner

import (
	"net/url"
	"strings"
)

// Normalizes protocol prefixes, trailing slashes, and case.
func normalizeHost(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimSuffix(s, "/")
	if strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://") {
		if u, err := url.Parse(s); err == nil && u.Host != "" {
			return strings.ToLower(u.Host)
		}
	}
	return strings.ToLower(s)
}
