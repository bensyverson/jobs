package job

import (
	"html"
	"strings"
)

// The inline pass runs over paragraph text — inside paragraphs and inside
// list items, never inside a fenced block, which stays verbatim. It renders
// three things and nothing else:
//
//   - Code spans. A run of backticks opens a span that the next run of the
//     same length closes; the content is escaped inside <code>. An unclosed
//     run is literal, as are the backticks of a run that closes nothing.
//   - Links. [text](url) becomes <a href> only when the URL is http(s) or a
//     site-relative path starting with a single "/". Any other target — a
//     javascript: URL, a protocol-relative "//host", a bare word — leaves
//     the "[" as a literal character and scanning continues after it, so
//     the source shows through unchanged. Link text gets the inline pass
//     with links switched off: code spans render, nested links do not.
//   - Id autolinks. A maximal run of [A-Za-z0-9] bounded by non-alphanumerics
//     is a candidate; ProseLinks is the only thing that decides whether it
//     links. A candidate shorter than four characters links only inside a
//     code span, because a three-character word is a word — that is what
//     keeps criterion ids (three characters) from turning every "the" in a
//     note into a link, while task ids (six) link bare as well.
//
// The console renderer does none of this: RenderProseText leaves every
// inline construct as written.

// ProseLinks maps a short id to the URL the inline pass links it to. It is
// a plain map rather than a struct because that is the entire contract —
// the store decides membership (see ResolveProseLinks), the renderer only
// looks ids up. It keeps the shared fixture's `links` field a JSON object
// of strings and the JS twin's argument a plain object, and the one
// distinction the renderer needs beyond the URL — bare versus code-span-only
// — follows from the length of the id, a property of the prose rather than
// of the store. A nil map resolves nothing.
type ProseLinks map[string]string

// proseBareIDMinLen is the shortest candidate that may link outside a code
// span. Criterion short ids are three characters (criterionShortIDLen) and
// collide with ordinary words; task short ids are six (shortIDLen).
const proseBareIDMinLen = 4

// renderInlineHTML appends text to sb with the inline pass applied. With
// allowLinks false — inside link text — neither [](…) nor an id autolink
// fires, so no <a> can nest inside another.
func renderInlineHTML(sb *strings.Builder, text string, links ProseLinks, allowLinks bool) {
	plain := 0
	for i := 0; i < len(text); {
		switch text[i] {
		case '`':
			n := runLen(text, i, '`')
			closeAt := closingBacktickRun(text, i+n, n)
			if closeAt < 0 {
				// Nothing closes this run: the backticks are literal, and
				// skipping past them stops a later run re-opening inside it.
				i += n
				continue
			}
			writeInlinePlain(sb, text[plain:i], links, allowLinks)
			writeCodeSpan(sb, text[i+n:closeAt], links, allowLinks)
			i = closeAt + n
			plain = i
		case '[':
			if !allowLinks {
				i++
				continue
			}
			linkText, url, end, ok := parseInlineLink(text, i)
			if !ok || !proseURLAllowed(url) {
				i++
				continue
			}
			writeInlinePlain(sb, text[plain:i], links, allowLinks)
			sb.WriteString(`<a href="`)
			sb.WriteString(html.EscapeString(url))
			sb.WriteString(`">`)
			renderInlineHTML(sb, linkText, links, false)
			sb.WriteString(`</a>`)
			i = end
			plain = i
		default:
			i++
		}
	}
	writeInlinePlain(sb, text[plain:], links, allowLinks)
}

// runLen counts the run of c starting at i.
func runLen(s string, i int, c byte) int {
	n := 0
	for i+n < len(s) && s[i+n] == c {
		n++
	}
	return n
}

// closingBacktickRun finds the index of the next run of exactly n backticks
// at or after from, or -1. A longer or shorter run neither closes the span
// nor is scanned into.
func closingBacktickRun(s string, from, n int) int {
	for j := from; j < len(s); {
		if s[j] != '`' {
			j++
			continue
		}
		m := runLen(s, j, '`')
		if m == n {
			return j
		}
		j += m
	}
	return -1
}

// parseInlineLink reads [text](url) starting at the "[" at i. Brackets do
// not nest: the first "]" ends the text.
func parseInlineLink(s string, i int) (text, url string, end int, ok bool) {
	closeAt := strings.IndexByte(s[i+1:], ']')
	if closeAt < 0 {
		return "", "", 0, false
	}
	closeAt += i + 1
	if closeAt+1 >= len(s) || s[closeAt+1] != '(' {
		return "", "", 0, false
	}
	paren := strings.IndexByte(s[closeAt+2:], ')')
	if paren < 0 {
		return "", "", 0, false
	}
	paren += closeAt + 2
	return s[i+1 : closeAt], s[closeAt+2 : paren], paren + 1, true
}

// proseURLAllowed reports whether a link target may become an href. A
// protocol-relative "//host" is refused with every other scheme: it is an
// external target wearing a relative path's clothes.
func proseURLAllowed(url string) bool {
	if strings.HasPrefix(url, "//") {
		return false
	}
	if strings.HasPrefix(url, "/") {
		return true
	}
	return hasASCIIPrefixFold(url, "http://") || hasASCIIPrefixFold(url, "https://")
}

// hasASCIIPrefixFold is strings.HasPrefix, case-insensitive over ASCII only.
// Unicode-aware case folding would diverge from the JS twin's toLowerCase.
func hasASCIIPrefixFold(s, prefix string) bool {
	if len(s) < len(prefix) {
		return false
	}
	for i := 0; i < len(prefix); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		if c != prefix[i] {
			return false
		}
	}
	return true
}

// writeCodeSpan writes <code>escaped</code>, wrapped in the id's link when
// the whole span is one candidate the resolver recognises. The <a> wraps
// the <code> so the monospace run stays visually intact.
func writeCodeSpan(sb *strings.Builder, content string, links ProseLinks, allowLinks bool) {
	url := ""
	if allowLinks && isProseCandidate(content) {
		url = links[content]
	}
	if url != "" {
		sb.WriteString(`<a href="`)
		sb.WriteString(html.EscapeString(url))
		sb.WriteString(`">`)
	}
	sb.WriteString("<code>")
	sb.WriteString(html.EscapeString(content))
	sb.WriteString("</code>")
	if url != "" {
		sb.WriteString("</a>")
	}
}

// writeInlinePlain escapes a run of plain text, linking the candidate
// tokens the resolver recognises.
func writeInlinePlain(sb *strings.Builder, text string, links ProseLinks, allowLinks bool) {
	if text == "" {
		return
	}
	if !allowLinks || len(links) == 0 {
		sb.WriteString(html.EscapeString(text))
		return
	}
	start := 0
	for i := 0; i <= len(text); i++ {
		if i < len(text) && isProseIDByte(text[i]) {
			continue
		}
		// text[start:i] is a maximal alphanumeric run (possibly empty).
		if i > start {
			token := text[start:i]
			if url := links[token]; url != "" && len(token) >= proseBareIDMinLen {
				sb.WriteString(`<a href="`)
				sb.WriteString(html.EscapeString(url))
				sb.WriteString(`">`)
				sb.WriteString(token)
				sb.WriteString(`</a>`)
			} else {
				sb.WriteString(token)
			}
		}
		if i < len(text) {
			sb.WriteString(html.EscapeString(text[i : i+1]))
		}
		start = i + 1
	}
}

func isProseIDByte(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

// isProseCandidate reports whether s is one candidate token: a non-empty
// run of [A-Za-z0-9] and nothing else.
func isProseCandidate(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if !isProseIDByte(s[i]) {
			return false
		}
	}
	return true
}
