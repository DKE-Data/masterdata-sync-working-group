package main

import (
	"fmt"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// The model the emitter writes, in the style of openapi.yaml: block
// mappings, short lists of scalars inline, descriptions folded at 80 columns.
type (
	kv struct {
		k string
		v any
	}
	omap    []kv
	flowSeq []any // written [a, b] when it fits on the line
	// folded is written as a folded block scalar, in the lines it was read
	// from where known, so that the profile decides where lines break.
	folded struct {
		text  string
		lines []string
	}
)

const width = 80

func emitMap(b *strings.Builder, m omap, indent int) {
	pad := strings.Repeat(" ", indent)
	for _, e := range m {
		emitEntry(b, pad, indent, e)
	}
}

// emitEntry writes one key and its value. lead is what precedes the key on
// its line; nested content is indented by two from indent.
func emitEntry(b *strings.Builder, lead string, indent int, e kv) {
	key := e.k
	if !plain(key, false) {
		key = quote(key, false)
	}
	switch v := e.v.(type) {
	case omap:
		if len(v) == 0 {
			fmt.Fprintf(b, "%s%s: {}\n", lead, key)
			return
		}
		fmt.Fprintf(b, "%s%s:\n", lead, key)
		emitMap(b, v, indent+2)
	case flowSeq:
		if line, ok := flow(v); ok && len(lead)+len(key)+2+len(line) <= width {
			fmt.Fprintf(b, "%s%s: %s\n", lead, key, line)
			return
		}
		emitEntry(b, lead, indent, kv{e.k, []any(v)})
	case []any:
		if len(v) == 0 {
			fmt.Fprintf(b, "%s%s: []\n", lead, key)
			return
		}
		fmt.Fprintf(b, "%s%s:\n", lead, key)
		emitSeq(b, v, indent+2)
	case folded:
		chomp := ">"
		if !strings.HasSuffix(v.text, "\n") {
			chomp = ">-"
		}
		fmt.Fprintf(b, "%s%s: %s\n", lead, key, chomp)
		if v.lines == nil {
			emitFolded(b, strings.TrimSuffix(v.text, "\n"), indent+2)
			return
		}
		pad := strings.Repeat(" ", indent+2)
		for _, l := range v.lines {
			if l != "" {
				l = pad + l
			}
			b.WriteString(l + "\n")
		}
	default:
		fmt.Fprintf(b, "%s%s: %s\n", lead, key, scalarText(v, false))
	}
}

func emitSeq(b *strings.Builder, items []any, indent int) {
	pad := strings.Repeat(" ", indent)
	for _, it := range items {
		switch v := it.(type) {
		case omap:
			if len(v) == 0 {
				fmt.Fprintf(b, "%s- {}\n", pad)
				continue
			}
			emitEntry(b, pad+"- ", indent+2, v[0])
			for _, e := range v[1:] {
				emitEntry(b, pad+"  ", indent+2, e)
			}
		case flowSeq, []any, folded:
			panic("emit: nested sequence or folded scalar in a sequence")
		default:
			fmt.Fprintf(b, "%s- %s\n", pad, scalarText(v, false))
		}
	}
}

// emitFolded writes text so that a folded scalar reads it back: words wrap
// at width, and each line break of the text is an empty line.
func emitFolded(b *strings.Builder, text string, indent int) {
	pad := strings.Repeat(" ", indent)
	for i, para := range strings.Split(text, "\n") {
		if i > 0 {
			b.WriteString("\n")
		}
		// Break only at single spaces: folding turns a line break back into
		// one space.
		var words []string
		for _, w := range strings.Split(para, " ") {
			if n := len(words); n > 0 && (w == "" || strings.HasSuffix(words[n-1], " ")) {
				words[n-1] += " " + w
				continue
			}
			words = append(words, w)
		}
		line := ""
		for _, w := range words {
			if line != "" && indent+len(line)+1+len(w) > width {
				b.WriteString(pad + line + "\n")
				line = ""
			}
			if line != "" {
				line += " "
			}
			line += w
		}
		if line != "" {
			b.WriteString(pad + line + "\n")
		}
	}
}

func flow(items flowSeq) (string, bool) {
	parts := make([]string, len(items))
	for i, it := range items {
		switch it.(type) {
		case omap, flowSeq, []any, folded:
			return "", false
		}
		parts[i] = scalarText(it, true)
	}
	return "[" + strings.Join(parts, ", ") + "]", true
}

func scalarText(v any, inFlow bool) string {
	switch x := v.(type) {
	case string:
		return quote(x, inFlow)
	case bool:
		return strconv.FormatBool(x)
	case int:
		return strconv.Itoa(x)
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case nil:
		return "null"
	}
	panic(fmt.Sprintf("emit: unsupported scalar %T", v))
}

// quote writes s plain where YAML reads it back as the same string, and
// single-quoted otherwise.
func quote(s string, inFlow bool) string {
	// Plain, but hard to read as a string, such as a pattern.
	if plain(s, inFlow) && !strings.ContainsAny(s, "^$\\[]{}") {
		return s
	}
	if strings.ContainsAny(s, "\n\t\\") {
		return strconv.Quote(s)
	}
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

func plain(s string, inFlow bool) bool {
	if s == "" || s != strings.TrimSpace(s) || strings.ContainsAny(s, "\n\t") {
		return false
	}
	if strings.ContainsAny(s[:1], "-?:,[]{}#&*!|>'\"%@`") {
		return false
	}
	if strings.Contains(s, ": ") || strings.Contains(s, " #") || strings.HasSuffix(s, ":") {
		return false
	}
	if inFlow && strings.ContainsAny(s, ",[]{}:#") {
		return false
	}
	var v any
	if err := yaml.Unmarshal([]byte(s), &v); err != nil {
		return false
	}
	return v == s
}
