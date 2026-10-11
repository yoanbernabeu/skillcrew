// Package skill implements the parts of the Agent Skills specification
// (https://agentskills.io/specification) that Skillcrew relies on.
package skill

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"gopkg.in/yaml.v3"

	"github.com/yoanbernabeu/skillcrew/internal/fsx"
)

// FileName is the entry point of every skill directory.
const FileName = "SKILL.md"

// Placeholder is the line "skillcrew skill new" writes where the
// instructions of a new skill go.
const Placeholder = "TODO: write the instructions the agent follows when it uses this skill."

const (
	maxNameLength = 64
	// MaxDescriptionLength is the specification limit on descriptions, in characters.
	MaxDescriptionLength = 1024
)

var (
	namePattern = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
	delimiter   = []byte("---")
)

// Frontmatter holds the SKILL.md metadata Skillcrew cares about.
type Frontmatter struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
}

// ParseFrontmatter extracts the YAML frontmatter of a SKILL.md file.
func ParseFrontmatter(content []byte) (Frontmatter, error) {
	raw, _, err := splitFrontmatter(content)
	if err != nil {
		return Frontmatter{}, err
	}
	var fm Frontmatter
	if err := yaml.Unmarshal(raw, &fm); err != nil {
		return Frontmatter{}, fmt.Errorf("invalid frontmatter: %w", err)
	}
	return fm, nil
}

// splitFrontmatter returns the frontmatter bytes and the offset where they start.
func splitFrontmatter(content []byte) (raw []byte, start int, err error) {
	if !bytes.HasPrefix(content, delimiter) {
		return nil, 0, errors.New("missing frontmatter")
	}
	start = bytes.IndexByte(content, '\n') + 1
	if start == 0 {
		return nil, 0, errors.New("unterminated frontmatter")
	}
	for offset := start; offset < len(content); {
		end := bytes.IndexByte(content[offset:], '\n')
		line := content[offset:]
		if end >= 0 {
			line = content[offset : offset+end]
		}
		if bytes.Equal(bytes.TrimRight(line, " \r"), delimiter) {
			return content[start:offset], start, nil
		}
		if end < 0 {
			break
		}
		offset += end + 1
	}
	return nil, 0, errors.New("unterminated frontmatter")
}

// ValidateName checks a skill name against the specification.
func ValidateName(name string) error {
	if len(name) == 0 || len(name) > maxNameLength {
		return fmt.Errorf("name %q must be 1 to %d characters", name, maxNameLength)
	}
	if !namePattern.MatchString(name) {
		return fmt.Errorf("name %q must use lowercase letters, digits and single hyphens", name)
	}
	if name == "synced" {
		return fmt.Errorf("name %q is reserved by Claude Code", name)
	}
	return nil
}

// Validate checks the skill stored in dir and returns every problem found.
func Validate(fsys fs.FS, dir string) []error {
	content, err := fs.ReadFile(fsys, path.Join(dir, FileName))
	if err != nil {
		return []error{fmt.Errorf("%s: cannot read %s: %w", dir, FileName, err)}
	}
	fm, err := ParseFrontmatter(content)
	if err != nil {
		return []error{fmt.Errorf("%s: %w", dir, err)}
	}
	var errs []error
	if err := ValidateName(fm.Name); err != nil {
		errs = append(errs, fmt.Errorf("%s: %w", dir, err))
	} else if fm.Name != path.Base(dir) {
		errs = append(errs, fmt.Errorf("%s: name %q must match the directory name", dir, fm.Name))
	}
	if n := utf8.RuneCountInString(fm.Description); n == 0 || n > MaxDescriptionLength {
		errs = append(errs, fmt.Errorf("%s: description must be 1 to %d characters", dir, MaxDescriptionLength))
	}
	return errs
}

var nameLine = regexp.MustCompile(`(?m)^name:[^\n]*$`)

// RenameFrontmatter rewrites the name field of a SKILL.md file, leaving the rest untouched.
func RenameFrontmatter(content []byte, name string) ([]byte, error) {
	raw, start, err := splitFrontmatter(content)
	if err != nil {
		return nil, err
	}
	loc := nameLine.FindIndex(raw)
	if loc == nil {
		return nil, errors.New("frontmatter has no name field")
	}
	var out bytes.Buffer
	out.Write(content[:start+loc[0]])
	out.WriteString("name: " + name)
	out.Write(content[start+loc[1]:])
	return out.Bytes(), nil
}

// Ignored reports whether a file is operating system metadata (e.g. the
// Finder's .DS_Store), which never counts as skill content.
func Ignored(name string) bool { return name == ".DS_Store" }

// HashDir returns a content hash of the files under root. It covers relative
// paths, contents and the executable bit, so it does not depend on where the
// directory lives. Symbolic links are hashed by target, never followed.
func HashDir(fsys fs.FS, root string) (string, error) {
	type entry struct {
		path string
		kind string
		sum  [sha256.Size]byte
	}
	var entries []entry
	err := fs.WalkDir(fsys, root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || Ignored(d.Name()) {
			return err
		}
		rel := p
		if root != "." {
			rel = strings.TrimPrefix(p, root+"/")
		}
		e := entry{path: rel}
		switch t := d.Type(); {
		case t&fs.ModeSymlink != 0:
			target, err := fs.ReadLink(fsys, p)
			if err != nil {
				return err
			}
			e.kind, e.sum = "link", sha256.Sum256([]byte(target))
		case t.IsRegular():
			info, err := d.Info()
			if err != nil {
				return err
			}
			data, err := fs.ReadFile(fsys, p)
			if err != nil {
				return err
			}
			e.kind, e.sum = "file", sha256.Sum256(data)
			if info.Mode()&0o111 != 0 {
				e.kind = "exec"
			}
		default:
			e.kind = "special:" + t.String()
		}
		entries = append(entries, e)
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].path < entries[j].path })
	h := sha256.New()
	for _, e := range entries {
		fmt.Fprintf(h, "%s\x00%s\x00%x\n", e.path, e.kind, e.sum)
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
}

// CopyAs copies the skill in src to dst and names it after dst's base name:
// the SKILL.md name is rewritten if needed, then the copy is validated.
func CopyAs(src, dst string) error {
	name := filepath.Base(dst)
	if err := ValidateName(name); err != nil {
		return err
	}
	if err := fsx.CopyDir(src, dst); err != nil {
		return err
	}
	file := filepath.Join(dst, FileName)
	content, err := os.ReadFile(file)
	if err != nil {
		return err
	}
	if fm, err := ParseFrontmatter(content); err == nil && fm.Name != name {
		if content, err = RenameFrontmatter(content, name); err != nil {
			return err
		}
		// file is dst/SKILL.md, and dst was validated above.
		if err := os.WriteFile(file, content, 0o644); err != nil { //nolint:gosec // G703: dst is a validated skill directory
			return err
		}
	}
	return errors.Join(Validate(os.DirFS(filepath.Dir(dst)), name)...)
}
