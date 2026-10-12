package l2tpagent

import (
	"regexp"
	"strconv"
	"strings"
)

// ocservVersion is the version on the first line of `ocserv --version`,
// which 1.1 writes as "ocserv 1.1.6" and 1.3 as "OpenConnect VPN Server
// 1.3.0".
var ocservVersion = regexp.MustCompile(`\b(\d+)\.(\d+)(?:\.\d+)?\b`)

// camouflageIn reports whether a version line is of an ocserv that can hide
// itself: 1.2 or later.
func camouflageIn(versionOutput string) bool {
	first, _, _ := strings.Cut(versionOutput, "\n")
	m := ocservVersion.FindStringSubmatch(first)
	if m == nil {
		return false
	}
	major, _ := strconv.Atoi(m[1])
	minor, _ := strconv.Atoi(m[2])
	return major > 1 || major == 1 && minor >= 2
}
