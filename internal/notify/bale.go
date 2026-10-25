package notify

import (
	"regexp"
	"strings"
)

// Bale (bale.ai) is the Iranian messenger whose bot API is Telegram's, with
// small differences. The same notifications, reports and backups reach it
// through a second channel, with its own bot, chats and choices.
//
// What differs, and is handled here:
//
//   - The address: https://tapi.bale.ai/bot<token>/<method>.
//   - Formatting: there is no parse_mode. Every message is Markdown, and bold
//     and italic only render with a space before the opening marker and after
//     the closing one -- " *bold* ", " _italic_ ".
const (
	// PlatformTelegram is the zero value, so every stored configuration from
	// before Bale is Telegram's, as it was.
	PlatformTelegram = ""
	PlatformBale     = "bale"
)

// BaleAPI is Bale's bot API.
const BaleAPI = "https://tapi.bale.ai"

// PlatformName is how a platform is named in the log.
func PlatformName(p string) string {
	if p == PlatformBale {
		return "Bale"
	}
	return "Telegram"
}

// Name is this configuration's platform, for the log.
func (c Config) Name() string { return PlatformName(c.Platform) }

var (
	mdBold   = regexp.MustCompile(`\*([^*\n]+)\*`)
	mdItalic = regexp.MustCompile(`(^|[^\w])_([^_\n]+)_`)
)

// BaleMarkdown rewrites what is written for Telegram's legacy Markdown --
// *bold*, _italic_ -- as Bale renders it: each marker with a space outside
// it, where the text has none already.
func BaleMarkdown(s string) string {
	s = mdBold.ReplaceAllString(s, "\x00*$1*\x00")
	s = mdItalic.ReplaceAllString(s, "$1\x00_${2}_\x00")
	return padMarkers(s)
}

// padMarkers turns each \x00 into the space Bale needs, except where there is
// one already, or the line starts or ends there.
func padMarkers(s string) string {
	var b strings.Builder
	b.Grow(len(s) + 8)
	prev := byte('\n')
	for i := 0; i < len(s); i++ {
		if s[i] != 0 {
			b.WriteByte(s[i])
			prev = s[i]
			continue
		}
		next := byte('\n')
		if i+1 < len(s) {
			next = s[i+1]
		}
		if next == 0 && i+2 < len(s) {
			next = s[i+2]
		}
		// Before an opening marker the character behind matters, after a
		// closing one the character ahead; the other side is the marker.
		if prev == ' ' || prev == '\n' || next == ' ' || next == '\n' {
			continue
		}
		b.WriteByte(' ')
		prev = ' '
	}
	return b.String()
}

// render is text as this platform's chat takes it.
func (c Config) render(text string) string {
	if c.Platform == PlatformBale {
		return BaleMarkdown(text)
	}
	return text
}
