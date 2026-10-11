package scaffold

import (
	"fmt"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/yoanbernabeu/skillcrew/internal/registry"
)

// addRule returns the governance file src with "id: status" added to the
// mapping at path (["skills"], or ["teams", team, "skills"]), creating the
// missing mappings of path. It edits the text rather than re-encoding the
// YAML, so comments, blank lines, quoting and line endings are kept. It also
// returns the keys of path it created. Layouts it cannot edit safely (an
// inline mapping on the path, several documents) are refused with a request
// to edit the file by hand; the caller checks the result by parsing it.
func addRule(src []byte, path []string, id string, status registry.Status) ([]byte, []string, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(src, &doc); err != nil {
		return nil, nil, fmt.Errorf("%s: %w", registry.GovernanceFile, err)
	}
	e := newEditor(string(src))
	entry := quote(id) + ": " + string(status)

	if len(doc.Content) == 0 {
		e.append(render(path, entry, 0))
		return e.bytes(), path, nil
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode || root.Style&yaml.FlowStyle != 0 {
		return nil, nil, byHand("the top level is not a block mapping")
	}
	node := root
	for i, key := range path {
		k, v := lookup(node, key)
		if k == nil {
			if node == root {
				e.append(render(path[i:], entry, 0)) // new top-level keys go at the end
			} else {
				e.insert(e.end(node), render(path[i:], entry, node.Content[0].Column-1))
			}
			return e.bytes(), path[i:], nil
		}
		where := strings.Join(path[:i+1], ".")
		switch {
		case v.Kind == yaml.ScalarNode && v.Tag == "!!null" && v.Value == "":
			e.insert(k.Line, render(path[i+1:], entry, k.Column+1))
			return e.bytes(), path[i+1:], nil
		case v.Kind == yaml.MappingNode && v.Style&yaml.FlowStyle != 0 && len(v.Content) == 0:
			if !e.removeEmptyFlow(v) {
				return nil, nil, byHand(where + " is an inline mapping")
			}
			e.insert(k.Line, render(path[i+1:], entry, k.Column+1))
			return e.bytes(), path[i+1:], nil
		case v.Kind == yaml.AliasNode:
			return nil, nil, byHand(where + " is an alias")
		case v.Kind != yaml.MappingNode:
			return nil, nil, byHand(where + " is not a mapping")
		case v.Style&yaml.FlowStyle != 0:
			return nil, nil, byHand(where + " is an inline mapping")
		}
		node = v
	}
	if k, _ := lookup(node, id); k != nil {
		return nil, nil, fmt.Errorf("%s is already listed in %s at line %d; edit it there", id, registry.GovernanceFile, k.Line)
	}
	e.insert(e.end(node), render(nil, entry, node.Content[0].Column-1))
	return e.bytes(), nil, nil
}

func byHand(reason string) error {
	return fmt.Errorf("cannot add the rule automatically (%s); edit skillcrew.yaml by hand", reason)
}

// editor edits a file line by line. Lines are stored without their line
// ending; eol is the file's, taken from its first line.
type editor struct {
	lines []string
	eol   string
	final bool // the file ends with a line ending
}

func newEditor(text string) *editor {
	e := &editor{eol: "\n", final: strings.HasSuffix(text, "\n") || text == ""}
	if i := strings.IndexByte(text, '\n'); i > 0 && text[i-1] == '\r' {
		e.eol = "\r\n"
	}
	text = strings.TrimSuffix(strings.TrimSuffix(text, "\n"), "\r")
	if text != "" {
		e.lines = strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	}
	return e
}

func (e *editor) bytes() []byte {
	out := strings.Join(e.lines, e.eol)
	if len(e.lines) > 0 {
		out += e.eol // an edited file always ends with a line ending
	}
	return []byte(out)
}

// insert inserts lines after line at (1-based; 0 inserts at the top).
func (e *editor) insert(at int, lines []string) {
	e.lines = append(e.lines[:at], append(lines, e.lines[at:]...)...)
}

// append adds lines at the end of the file, after a blank line.
func (e *editor) append(lines []string) {
	if n := len(e.lines); n > 0 && strings.TrimSpace(e.lines[n-1]) != "" {
		lines = append([]string{""}, lines...)
	}
	e.insert(len(e.lines), lines)
}

// end returns the last line (1-based) of a block mapping: the last line,
// after its last key, that is indented more than its keys. Blank lines and
// comments are not part of the mapping, so a trailing comment stays after
// what is inserted. Values spanning several lines (block scalars, wrapped
// scalars, inline collections) are covered, since their lines are indented
// more than the key.
func (e *editor) end(m *yaml.Node) int {
	last := m.Content[len(m.Content)-2]
	indent := m.Content[0].Column - 1
	end := last.Line
	for i := last.Line; i < len(e.lines); i++ {
		line := e.lines[i]
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if len(line)-len(strings.TrimLeft(line, " ")) <= indent {
			break
		}
		end = i + 1
	}
	return end
}

// emptyFlow matches an empty inline mapping, "{}" or "{ }".
var emptyFlow = regexp.MustCompile(`^\{\s*\}`)

// removeEmptyFlow removes the empty inline mapping v from its line, with
// the space before it.
func (e *editor) removeEmptyFlow(v *yaml.Node) bool {
	line, col := v.Line-1, v.Column-1
	if line >= len(e.lines) || col > len(e.lines[line]) {
		return false
	}
	loc := emptyFlow.FindStringIndex(e.lines[line][col:])
	if loc == nil {
		return false
	}
	start := col
	if start > 0 && e.lines[line][start-1] == ' ' {
		start--
	}
	e.lines[line] = e.lines[line][:start] + e.lines[line][col+loc[1]:]
	return true
}

// render returns the lines of the nested mappings keys, holding entry, at
// the given indentation.
func render(keys []string, entry string, indent int) []string {
	var lines []string
	for i, k := range keys {
		lines = append(lines, strings.Repeat(" ", indent+2*i)+quote(k)+":")
	}
	return append(lines, strings.Repeat(" ", indent+2*len(keys))+entry)
}

// lookup returns the key and value nodes of key in a mapping.
func lookup(m *yaml.Node, key string) (*yaml.Node, *yaml.Node) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i], m.Content[i+1]
		}
	}
	return nil, nil
}
