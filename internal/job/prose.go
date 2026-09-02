package job

import (
	"fmt"
	"html"
	"strconv"
	"strings"
)

// Descriptions, notes, completion notes and cancel reasons are markdown
// prose: a single newline inside a paragraph is a soft break, a blank line
// ends a paragraph, list items keep their own line, and fenced code is
// verbatim. ParseProse turns text into typed blocks; RenderProseText and
// RenderProseHTML consume the same blocks so the console and the dashboard
// agree. Inline syntax (backticks, emphasis, links) is left as written.
//
// One deliberate departure from CommonMark: an unindented line directly
// after a list item starts a new paragraph instead of lazily continuing the
// item, because "Remaining:" written straight under a bullet list is far
// more common in a note than an unindented wrapped bullet.

// ProseKind names the block types ParseProse produces.
type ProseKind int

const (
	ProseParagraph ProseKind = iota
	ProseList
	ProseCode
)

// ProseBlock is one block of parsed prose. Which fields are set depends on
// Kind: a paragraph carries Lines (one per hard break), a code block carries
// Lines verbatim plus Fence and Info, and a list carries Items.
type ProseBlock struct {
	Kind    ProseKind
	Lines   []string
	Fence   string // opening fence as written, e.g. "```" or "~~~"
	Info    string // fence info string, e.g. "go"
	Ordered bool
	Start   int  // first number of an ordered list
	Loose   bool // items separated by blank lines render as paragraphs
	Items   []ProseItem
}

// ProseItem is one list item: the blocks nested under its marker.
type ProseItem []ProseBlock

// ParseProse splits text into blocks. See the package comment above for the
// rules.
func ParseProse(text string) []ProseBlock {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	if strings.TrimSpace(text) == "" {
		return nil
	}
	return parseProseLines(strings.Split(text, "\n"))
}

// listMarker describes the marker at the head of a line, if any.
type listMarker struct {
	ok      bool
	ordered bool
	number  int
	indent  int // columns before the marker
	content int // columns before the item's content
	text    string
}

func markerAt(line string) listMarker {
	indent := len(line) - len(strings.TrimLeft(line, " "))
	rest := line[indent:]
	if rest == "" {
		return listMarker{}
	}
	if c := rest[0]; c == '-' || c == '*' || c == '+' {
		if len(rest) > 1 && rest[1] == ' ' && strings.TrimSpace(rest[2:]) != "" {
			body := strings.TrimLeft(rest[2:], " ")
			return listMarker{ok: true, indent: indent, content: indent + len(rest) - len(body), text: body}
		}
		return listMarker{}
	}
	digits := 0
	for digits < len(rest) && digits < 9 && rest[digits] >= '0' && rest[digits] <= '9' {
		digits++
	}
	if digits == 0 || digits+1 >= len(rest) {
		return listMarker{}
	}
	if sep := rest[digits]; sep != '.' && sep != ')' {
		return listMarker{}
	}
	if rest[digits+1] != ' ' || strings.TrimSpace(rest[digits+2:]) == "" {
		return listMarker{}
	}
	n, _ := strconv.Atoi(rest[:digits])
	body := strings.TrimLeft(rest[digits+2:], " ")
	return listMarker{ok: true, ordered: true, number: n, indent: indent, content: indent + len(rest) - len(body), text: body}
}

// fenceAt reports the fence opening a line, its indent, and its info string.
func fenceAt(line string) (fence, info string, indent int, ok bool) {
	indent = len(line) - len(strings.TrimLeft(line, " "))
	rest := line[indent:]
	if rest == "" {
		return "", "", 0, false
	}
	c := rest[0]
	if c != '`' && c != '~' {
		return "", "", 0, false
	}
	n := 0
	for n < len(rest) && rest[n] == c {
		n++
	}
	if n < 3 {
		return "", "", 0, false
	}
	return rest[:n], strings.TrimSpace(rest[n:]), indent, true
}

func closesFence(line, fence string) bool {
	t := strings.TrimSpace(line)
	return len(t) >= len(fence) && strings.Trim(t, fence[:1]) == ""
}

func dedent(line string, n int) string {
	for i := 0; i < n && len(line) > 0 && line[0] == ' '; i++ {
		line = line[1:]
	}
	return line
}

func isBlank(line string) bool { return strings.TrimSpace(line) == "" }

func parseProseLines(lines []string) []ProseBlock {
	var blocks []ProseBlock
	i := 0
	for i < len(lines) {
		line := lines[i]
		if isBlank(line) {
			i++
			continue
		}
		if fence, info, indent, ok := fenceAt(line); ok {
			block := ProseBlock{Kind: ProseCode, Fence: fence, Info: info, Lines: []string{}}
			i++
			for i < len(lines) && !closesFence(lines[i], fence) {
				block.Lines = append(block.Lines, dedent(lines[i], indent))
				i++
			}
			i++ // past the closing fence, or past the end
			blocks = append(blocks, block)
			continue
		}
		if m := markerAt(line); m.ok {
			block, next := parseList(lines, i, m)
			blocks = append(blocks, block)
			i = next
			continue
		}
		para := ProseBlock{Kind: ProseParagraph}
		var current []string
		flush := func() {
			if len(current) > 0 {
				para.Lines = append(para.Lines, strings.Join(current, " "))
				current = nil
			}
		}
		for i < len(lines) && !isBlank(lines[i]) {
			l := lines[i]
			if _, _, _, ok := fenceAt(l); ok {
				break
			}
			if markerAt(l).ok {
				break
			}
			hard := strings.HasSuffix(l, "  ")
			l = strings.TrimSpace(l)
			if strings.HasSuffix(l, "\\") {
				hard = true
				l = strings.TrimRight(l[:len(l)-1], " \t")
			}
			current = append(current, l)
			if hard {
				flush()
			}
			i++
		}
		flush()
		blocks = append(blocks, para)
	}
	return blocks
}

// parseList consumes the list starting at lines[i] (whose marker is m) and
// returns the block and the index of the first line after it.
func parseList(lines []string, i int, m listMarker) (ProseBlock, int) {
	block := ProseBlock{Kind: ProseList, Ordered: m.ordered, Start: m.number}
	for i < len(lines) {
		m := markerAt(lines[i])
		if !m.ok || m.ordered != block.Ordered {
			break
		}
		item := []string{m.text}
		i++
		pendingBlank := 0
		for i < len(lines) {
			l := lines[i]
			if isBlank(l) {
				pendingBlank++
				i++
				continue
			}
			indent := len(l) - len(strings.TrimLeft(l, " "))
			if indent >= m.content {
				if pendingBlank > 0 {
					block.Loose = true
				}
				for ; pendingBlank > 0; pendingBlank-- {
					item = append(item, "")
				}
				item = append(item, dedent(l, m.content))
				i++
				continue
			}
			break
		}
		block.Items = append(block.Items, parseProseLines(item))
		if i < len(lines) {
			next := markerAt(lines[i])
			if !next.ok || next.ordered != block.Ordered || next.indent >= m.content {
				break
			}
			if pendingBlank > 0 {
				block.Loose = true
			}
		}
	}
	return block, i
}

// RenderProseText renders prose for the console: paragraphs reflowed onto
// one line each, blocks separated by a blank line, lists and fences kept.
func RenderProseText(text string) string {
	return renderBlocksText(ParseProse(text), "", false)
}

// RenderProseTextIndented is RenderProseText with every line prefixed by
// indent. With hanging set, the first line is left bare for a caller that
// has already written a label in front of it.
func RenderProseTextIndented(text, indent string, hanging bool) string {
	out := renderBlocksText(ParseProse(text), indent, false)
	if hanging {
		out = strings.TrimPrefix(out, indent)
	}
	return out
}

func renderBlocksText(blocks []ProseBlock, indent string, tight bool) string {
	var parts []string
	for _, b := range blocks {
		parts = append(parts, renderBlockText(b, indent))
	}
	sep := "\n\n"
	if tight {
		sep = "\n"
	}
	return strings.Join(parts, sep)
}

func renderBlockText(b ProseBlock, indent string) string {
	switch b.Kind {
	case ProseCode:
		out := []string{b.Fence + b.Info}
		out = append(out, b.Lines...)
		out = append(out, b.Fence)
		return indentLines(out, indent)
	case ProseList:
		var items []string
		for n, item := range b.Items {
			marker := "- "
			if b.Ordered {
				marker = strconv.Itoa(b.Start+n) + ". "
			}
			body := renderBlocksText(item, indent+strings.Repeat(" ", len(marker)), !b.Loose)
			items = append(items, indent+marker+strings.TrimPrefix(body, indent+strings.Repeat(" ", len(marker))))
		}
		sep := "\n"
		if b.Loose {
			sep = "\n\n"
		}
		return strings.Join(items, sep)
	default:
		return indentLines(b.Lines, indent)
	}
}

func indentLines(lines []string, indent string) string {
	out := make([]string, len(lines))
	for i, l := range lines {
		if l == "" {
			out[i] = ""
		} else {
			out[i] = indent + l
		}
	}
	return strings.Join(out, "\n")
}

// RenderProseHTML renders prose as escaped HTML: <p>, <ul>/<ol> and
// <pre><code>. Every character of the source is escaped; no raw HTML passes
// through.
func RenderProseHTML(text string) string {
	var sb strings.Builder
	renderBlocksHTML(&sb, ParseProse(text), false)
	return sb.String()
}

func renderBlocksHTML(sb *strings.Builder, blocks []ProseBlock, tight bool) {
	for _, b := range blocks {
		switch b.Kind {
		case ProseCode:
			sb.WriteString("<pre><code")
			if b.Info != "" {
				fmt.Fprintf(sb, ` class="language-%s"`, html.EscapeString(b.Info))
			}
			sb.WriteString(">")
			for _, l := range b.Lines {
				sb.WriteString(html.EscapeString(l))
				sb.WriteString("\n")
			}
			sb.WriteString("</code></pre>")
		case ProseList:
			tag := "ul"
			if b.Ordered {
				tag = "ol"
			}
			sb.WriteString("<" + tag)
			if b.Ordered && b.Start != 1 {
				fmt.Fprintf(sb, ` start="%d"`, b.Start)
			}
			sb.WriteString(">")
			for _, item := range b.Items {
				sb.WriteString("<li>")
				renderBlocksHTML(sb, item, !b.Loose)
				sb.WriteString("</li>")
			}
			sb.WriteString("</" + tag + ">")
		default:
			if !tight {
				sb.WriteString("<p>")
			}
			for i, l := range b.Lines {
				if i > 0 {
					sb.WriteString("<br>")
				}
				sb.WriteString(html.EscapeString(l))
			}
			if !tight {
				sb.WriteString("</p>")
			}
		}
	}
}
