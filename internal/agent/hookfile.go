package agent

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/pretty"
	"github.com/tidwall/sjson"

	"github.com/yoanbernabeu/skillcrew/internal/fsx"
)

// hookFile is an agent whose session-start hooks live in a JSON array.
type hookFile struct {
	name    string
	dir     string // config directory under home, used for detection
	file    string // hook file, relative to dir
	list    string // gjson path of the session-start array
	grouped bool   // entries are matcher groups holding a "hooks" array
	key     string // key holding the command in a hook
	entry   string // JSON of a new entry, with an empty command
	flag    string // sync flag, "--hook" when empty
	version bool   // ensure a top-level "version": 1
	owned   bool   // Skillcrew owns the whole file
}

// loc locates one of our hooks: list[group], or list[group].hooks[hook]
// for grouped files.
type loc struct{ group, hook int }

func (h *hookFile) Name() string { return h.name }

func (h *hookFile) Detected(home string) bool {
	info, err := os.Stat(filepath.Join(home, h.dir))
	return err == nil && info.IsDir()
}

func (h *hookFile) HookInstalled(home string) (bool, error) {
	doc, _, err := h.load(home)
	if err != nil {
		return false, err
	}
	return len(h.find(doc)) > 0, nil
}

func (h *hookFile) InstallHook(home, bin string) error {
	cmd := command(bin, h.flag)
	doc, mode, err := h.load(home)
	if err != nil {
		return err
	}
	if list := gjson.Get(doc, h.list); list.Exists() && !list.IsArray() {
		return fmt.Errorf("%s: %s is not an array", h.path(home), h.list)
	}
	out := doc
	if h.version && !gjson.Get(out, "version").Exists() {
		if out, err = sjson.Set(out, "version", 1); err != nil {
			return err
		}
	}
	locs := h.find(out)
	if len(locs) == 0 {
		at := loc{int(gjson.Get(out, h.list+".#").Int()), 0}
		if out, err = sjson.SetRaw(out, h.list+".-1", h.entry); err != nil {
			return err
		}
		locs = []loc{at}
	}
	if gjson.Get(out, h.at(locs[0])+"."+h.key).String() != cmd {
		if out, err = sjson.Set(out, h.at(locs[0])+"."+h.key, cmd); err != nil {
			return err
		}
	}
	if out, err = h.remove(out, locs[1:]); err != nil {
		return err
	}
	return h.save(home, doc, out, mode)
}

func (h *hookFile) UninstallHook(home string) error {
	if h.owned {
		if err := os.Remove(h.path(home)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		return nil
	}
	doc, mode, err := h.load(home)
	if err != nil {
		return err
	}
	locs := h.find(doc)
	if len(locs) == 0 {
		return nil
	}
	out, err := h.remove(doc, locs)
	if err != nil {
		return err
	}
	// Drop the containers we leave empty, up to (not including) the root.
	for p := h.list; p != ""; p = parent(p) {
		if !isEmpty(gjson.Get(out, p)) {
			break
		}
		if out, err = sjson.Delete(out, p); err != nil {
			return err
		}
	}
	return h.save(home, doc, out, mode)
}

func (h *hookFile) path(home string) string { return filepath.Join(home, h.dir, h.file) }

// at returns the gjson path of the hook at l.
func (h *hookFile) at(l loc) string {
	if h.grouped {
		return fmt.Sprintf("%s.%d.hooks.%d", h.list, l.group, l.hook)
	}
	return fmt.Sprintf("%s.%d", h.list, l.group)
}

// find returns our hooks in document order.
func (h *hookFile) find(doc string) []loc {
	list := gjson.Get(doc, h.list)
	if !list.IsArray() {
		return nil
	}
	var locs []loc
	for g, entry := range list.Array() {
		if !h.grouped {
			if isOurs(entry.Get(h.key).String()) {
				locs = append(locs, loc{g, 0})
			}
			continue
		}
		for i, hook := range entry.Get("hooks").Array() {
			if isOurs(hook.Get(h.key).String()) {
				locs = append(locs, loc{g, i})
			}
		}
	}
	return locs
}

// remove deletes the hooks at locs (in document order), and the matcher
// groups they leave empty.
func (h *hookFile) remove(doc string, locs []loc) (string, error) {
	var err error
	for i := len(locs) - 1; i >= 0; i-- {
		if doc, err = sjson.Delete(doc, h.at(locs[i])); err != nil {
			return "", err
		}
		if !h.grouped {
			continue
		}
		group := fmt.Sprintf("%s.%d", h.list, locs[i].group)
		if isEmpty(gjson.Get(doc, group+".hooks")) {
			if doc, err = sjson.Delete(doc, group); err != nil {
				return "", err
			}
		}
	}
	return doc, nil
}

// load reads the hook file. A missing or empty file reads as "{}"; invalid
// JSON or a non-object root is an error.
func (h *hookFile) load(home string) (string, fs.FileMode, error) {
	path := h.path(home)
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return "{}", 0o644, nil
	}
	if err != nil {
		return "", 0, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", 0, err
	}
	doc := string(data)
	if len(bytes.TrimSpace(data)) == 0 {
		doc = "{}"
	}
	if !gjson.Valid(doc) || !gjson.Parse(doc).IsObject() {
		return "", 0, fmt.Errorf("%s: invalid JSON object", path)
	}
	return doc, info.Mode().Perm(), nil
}

// save writes out when it differs from doc, re-indented without reordering
// keys.
func (h *hookFile) save(home, doc, out string, mode fs.FileMode) error {
	if out == doc {
		return nil
	}
	data := pretty.PrettyOptions([]byte(out), &pretty.Options{Width: 80, Indent: "  "})
	return fsx.WriteFileAtomic(h.path(home), data, mode)
}

// parent returns the parent of a gjson path, or "" at the top level.
func parent(p string) string {
	if i := strings.LastIndex(p, "."); i >= 0 {
		return p[:i]
	}
	return ""
}

// isEmpty reports whether v is an empty array or object.
func isEmpty(v gjson.Result) bool {
	return (v.IsArray() && len(v.Array()) == 0) || (v.IsObject() && len(v.Map()) == 0)
}
