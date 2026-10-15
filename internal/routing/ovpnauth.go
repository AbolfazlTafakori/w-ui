package routing

import "strings"

// InlineAuthUserPass reads the sign-in an OpenVPN profile carries in an
// <auth-user-pass> block -- the username on the first line, the password on
// the second -- as this panel's own profiles do and many others'.
func InlineAuthUserPass(profile string) (user, pass string, ok bool) {
	text := strings.ReplaceAll(profile, "\r\n", "\n")
	start := strings.Index(text, "<auth-user-pass>")
	end := strings.Index(text, "</auth-user-pass>")
	if start < 0 || end < start {
		return "", "", false
	}
	var lines []string
	for _, l := range strings.Split(text[start+len("<auth-user-pass>"):end], "\n") {
		if l = strings.TrimSpace(l); l != "" {
			lines = append(lines, l)
		}
	}
	if len(lines) < 2 {
		return "", "", false
	}
	return lines[0], lines[1], true
}
