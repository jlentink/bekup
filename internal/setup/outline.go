package setup

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// outline is a small, path-addressed line editor for the block-style YAML
// of the repository compose file (D-205). The deployment variants are
// overlays: a list of edits on the source lines. Nothing is parsed and
// re-serialized, so every line an edit does not touch stays byte-identical
// (comments included).
//
// A node is addressed by its keys joined with "/", e.g.
// "services/serve/networks/frontend". Keys are block-mapping keys (plain
// [A-Za-z0-9_.-] names) at the indentation of their siblings; sequence
// items and flow collections are not addressable. A node spans its key
// line and every following line indented deeper (comments and blank lines
// inside included), plus its leading comments: the comment lines directly
// above the key line at the key's own indentation.
type outline struct {
	lines []string
}

// maxOutlineLines bounds the editor's input (the compose file is ~800
// lines).
const maxOutlineLines = 20000

var outlineKeyRe = regexp.MustCompile(`^( *)([A-Za-z0-9_.-]+):(?:\s|$)`)

func newOutline(src string) (*outline, error) {
	lines := strings.Split(src, "\n")
	if len(lines) > maxOutlineLines {
		return nil, fmt.Errorf("outline: more than %d lines", maxOutlineLines)
	}
	for i, l := range lines {
		if strings.ContainsRune(l, '\t') && !isCommentLine(l) {
			return nil, fmt.Errorf("outline: line %d contains a tab", i+1)
		}
	}
	return &outline{lines: lines}, nil
}

func (o *outline) String() string { return strings.Join(o.lines, "\n") }

func indentOf(l string) int { return len(l) - len(strings.TrimLeft(l, " ")) }

func isBlankLine(l string) bool { return strings.TrimSpace(l) == "" }

func isCommentLine(l string) bool { return strings.HasPrefix(strings.TrimLeft(l, " \t"), "#") }

func isContentLine(l string) bool { return !isBlankLine(l) && !isCommentLine(l) }

// nodeSpan is a located node: lead is its first leading comment line (==
// key without comments), key its key line, end the exclusive end of its
// block.
type nodeSpan struct{ lead, key, end int }

// keyOf returns the key and indentation of a block-mapping key line.
func keyOf(l string) (string, int, bool) {
	m := outlineKeyRe.FindStringSubmatch(l)
	if m == nil {
		return "", 0, false
	}
	return m[2], len(m[1]), true
}

// blockEnd returns the exclusive end of the block of the key line at k,
// searching below hi: every following line indented deeper than the key,
// with comment lines deeper than the key directly after the last content
// line included; blank lines and shallower comments at the end are not.
func (o *outline) blockEnd(k, hi int) int {
	ind := indentOf(o.lines[k])
	last := k
	for i := k + 1; i < hi; i++ {
		l := o.lines[i]
		if isContentLine(l) {
			if indentOf(l) <= ind {
				break
			}
			last = i
		}
	}
	end := last + 1
	for end < hi && isCommentLine(o.lines[end]) && indentOf(o.lines[end]) > ind {
		end++
	}
	return end
}

// leadStart returns the first line of the leading comment block of the key
// line at k (not above lo).
func (o *outline) leadStart(k, lo int) int {
	ind := indentOf(o.lines[k])
	i := k
	for i > lo && isCommentLine(o.lines[i-1]) && indentOf(o.lines[i-1]) == ind {
		i--
	}
	return i
}

// find locates the node at path.
func (o *outline) find(path string) (nodeSpan, error) {
	if path == "" {
		return nodeSpan{}, errors.New("outline: empty path")
	}
	lo, hi := 0, len(o.lines)
	var s nodeSpan
	for _, part := range strings.Split(path, "/") {
		child := -1
		for i := lo; i < hi; i++ {
			if isContentLine(o.lines[i]) {
				child = indentOf(o.lines[i])
				break
			}
		}
		if child < 0 {
			return nodeSpan{}, fmt.Errorf("outline: %s: %q has no block children", path, part)
		}
		found := -1
		for i := lo; i < hi; i++ {
			k, ind, ok := keyOf(o.lines[i])
			if !ok || ind != child || k != part {
				continue
			}
			if found >= 0 {
				return nodeSpan{}, fmt.Errorf("outline: %s: key %q is not unique", path, part)
			}
			found = i
		}
		if found < 0 {
			return nodeSpan{}, fmt.Errorf("outline: %s: key %q not found", path, part)
		}
		s = nodeSpan{lead: o.leadStart(found, lo), key: found, end: o.blockEnd(found, hi)}
		lo, hi = found+1, s.end
	}
	return s, nil
}

// fragmentLines splits a fragment (one trailing newline dropped); an empty
// fragment has no lines.
func fragmentLines(text string) []string {
	text = strings.TrimSuffix(text, "\n")
	if text == "" {
		return nil
	}
	return strings.Split(text, "\n")
}

// splice replaces lines [from, to) with repl and then collapses a doubled
// blank line at the seam (left behind by removing a block between two
// blank lines).
func (o *outline) splice(from, to int, repl []string) {
	out := make([]string, 0, len(o.lines)-(to-from)+len(repl))
	out = append(out, o.lines[:from]...)
	out = append(out, repl...)
	out = append(out, o.lines[to:]...)
	if len(repl) == 0 && from < len(out) && isBlankLine(out[from]) && (from == 0 || isBlankLine(out[from-1])) {
		out = append(out[:from], out[from+1:]...)
	}
	o.lines = out
}

// checkFragmentIndent requires the first content line of a node fragment
// to start at the indentation of the node it replaces or follows.
func checkFragmentIndent(path string, frag []string, want int) error {
	for _, l := range frag {
		if isContentLine(l) {
			if got := indentOf(l); got != want {
				return fmt.Errorf("outline: %s: fragment indented %d, want %d", path, got, want)
			}
			return nil
		}
	}
	return nil
}

// remove deletes the node at path with its leading comments.
func (o *outline) remove(path string) error {
	s, err := o.find(path)
	if err != nil {
		return err
	}
	o.splice(s.lead, s.end, nil)
	return nil
}

// replace replaces the node at path, leading comments included, with the
// fragment (which brings its own comments).
func (o *outline) replace(path, text string) error {
	s, err := o.find(path)
	if err != nil {
		return err
	}
	frag := fragmentLines(text)
	if err := checkFragmentIndent(path, frag, indentOf(o.lines[s.key])); err != nil {
		return err
	}
	o.splice(s.lead, s.end, frag)
	return nil
}

// insertAfter inserts the fragment directly after the node at path (as a
// sibling at the node's indentation).
func (o *outline) insertAfter(path, text string) error {
	s, err := o.find(path)
	if err != nil {
		return err
	}
	frag := fragmentLines(text)
	if len(frag) == 0 {
		return fmt.Errorf("outline: %s: empty insert", path)
	}
	if err := checkFragmentIndent(path, frag, indentOf(o.lines[s.key])); err != nil {
		return err
	}
	o.splice(s.end, s.end, frag)
	return nil
}

// replaceLead replaces the leading comments of the node at path (an empty
// fragment removes them). The fragment may hold comments only.
func (o *outline) replaceLead(path, text string) error {
	s, err := o.find(path)
	if err != nil {
		return err
	}
	frag := fragmentLines(text)
	for _, l := range frag {
		if !isCommentLine(l) {
			return fmt.Errorf("outline: %s: a leading-comment fragment may hold comments only", path)
		}
	}
	o.splice(s.lead, s.key, frag)
	return nil
}

// replaceComment replaces the one free-standing comment block (a maximal
// run of comment lines) that contains a line whose text after "#" starts
// with marker. An empty fragment removes the block.
func (o *outline) replaceComment(marker, text string) error {
	if strings.TrimSpace(marker) == "" {
		return errors.New("outline: empty comment marker")
	}
	from, to := -1, -1
	for i := 0; i < len(o.lines); {
		if !isCommentLine(o.lines[i]) {
			i++
			continue
		}
		j := i
		hit := false
		for j < len(o.lines) && isCommentLine(o.lines[j]) {
			body := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(o.lines[j]), "#"))
			if strings.HasPrefix(body, marker) {
				hit = true
			}
			j++
		}
		if hit {
			if from >= 0 {
				return fmt.Errorf("outline: comment %q is not unique", marker)
			}
			from, to = i, j
		}
		i = j
	}
	if from < 0 {
		return fmt.Errorf("outline: comment %q not found", marker)
	}
	frag := fragmentLines(text)
	for _, l := range frag {
		if !isCommentLine(l) {
			return fmt.Errorf("outline: comment %q: the fragment may hold comments only", marker)
		}
	}
	o.splice(from, to, frag)
	return nil
}

// replaceLine replaces the one comment line that is exactly line with the
// fragment (comments only).
// renameInComment replaces old with repl in the one comment line that
// contains old (a file name in a header, for example).
func (o *outline) renameInComment(old, repl string) error {
	if old == "" {
		return errors.New("outline: empty rename")
	}
	at := -1
	for i, l := range o.lines {
		if !isCommentLine(l) || !strings.Contains(l, old) {
			continue
		}
		if at >= 0 || strings.Count(l, old) > 1 {
			return fmt.Errorf("outline: %q is not unique in the comments", old)
		}
		at = i
	}
	if at < 0 {
		return fmt.Errorf("outline: %q not found in the comments", old)
	}
	o.lines[at] = strings.Replace(o.lines[at], old, repl, 1)
	return nil
}

func (o *outline) replaceLine(line, text string) error {
	if !isCommentLine(line) {
		return fmt.Errorf("outline: line %q: only comment lines can be replaced", line)
	}
	at := -1
	for i, l := range o.lines {
		if l != line {
			continue
		}
		if at >= 0 {
			return fmt.Errorf("outline: line %q is not unique", line)
		}
		at = i
	}
	if at < 0 {
		return fmt.Errorf("outline: line %q not found", line)
	}
	frag := fragmentLines(text)
	if len(frag) == 0 {
		return fmt.Errorf("outline: line %q: empty replacement", line)
	}
	for _, l := range frag {
		if !isCommentLine(l) {
			return fmt.Errorf("outline: line %q: the fragment may hold comments only", line)
		}
	}
	o.splice(at, at+1, frag)
	return nil
}

// replaceHeader replaces the file's leading comment block.
func (o *outline) replaceHeader(text string) error {
	end := 0
	for end < len(o.lines) && isCommentLine(o.lines[end]) {
		end++
	}
	if end == 0 {
		return errors.New("outline: the file has no header comment")
	}
	frag := fragmentLines(text)
	if len(frag) == 0 {
		return errors.New("outline: empty header")
	}
	for _, l := range frag {
		if !isCommentLine(l) {
			return errors.New("outline: the header may hold comments only")
		}
	}
	o.splice(0, end, frag)
	return nil
}
