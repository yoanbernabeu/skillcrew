// Package registry reads a Skillcrew registry: a Claude Code marketplace
// repository with a skillcrew.yaml governance file at its root.
package registry

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/yoanbernabeu/skillcrew/internal/skill"
)

// MarketplaceFile is the Claude Code marketplace manifest.
const MarketplaceFile = ".claude-plugin/marketplace.json"

// Skill is a skill discovered in the registry.
type Skill struct {
	ID     string // "plugin:name"
	Plugin string
	Name   string
	Dir    string // directory relative to the registry root
}

// Registry is a parsed registry.
type Registry struct {
	Name       string
	Skills     []Skill           // sorted by ID
	Plugins    map[string]string // plugin name -> directory
	Governance Governance
	// Warnings concern every developer (e.g. ignored plugins); syncs show them.
	Warnings []string
	// layout holds findings about the repository layout that only matter to
	// administrators: Validate reports them, syncs do not.
	layout []Issue
}

type marketplace struct {
	Name     string `json:"name"`
	Metadata struct {
		PluginRoot string `json:"pluginRoot"`
	} `json:"metadata"`
	Plugins []struct {
		Name   string          `json:"name"`
		Source json.RawMessage `json:"source"`
		Skills json.RawMessage `json:"skills"`
	} `json:"plugins"`
}

// Load parses the registry stored in fsys.
func Load(fsys fs.FS) (*Registry, error) {
	reg, err := Discover(fsys)
	if err != nil {
		return nil, err
	}
	data, err := fs.ReadFile(fsys, GovernanceFile)
	if err != nil {
		return nil, fmt.Errorf("missing %s (run \"skillcrew registry init\"): %w", GovernanceFile, err)
	}
	if reg.Governance, err = parseGovernance(data); err != nil {
		return nil, fmt.Errorf("%s: %w", GovernanceFile, err)
	}
	return reg, nil
}

// Discover reads the marketplace and its skills, ignoring the governance file.
func Discover(fsys fs.FS) (*Registry, error) {
	data, err := fs.ReadFile(fsys, MarketplaceFile)
	if err != nil {
		return nil, fmt.Errorf("not a registry: %w", err)
	}
	var m marketplace
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("%s: %w", MarketplaceFile, err)
	}
	reg := &Registry{Name: m.Name, Plugins: map[string]string{}, Governance: Governance{Default: Available}}
	listed := map[string]bool{}
	for _, p := range m.Plugins {
		var source string
		if json.Unmarshal(p.Source, &source) == nil {
			if dir, err := pluginDir(m.Metadata.PluginRoot, source); err == nil {
				listed[dir] = true
			}
		}
		if !pluginName.MatchString(p.Name) {
			reg.Warnings = append(reg.Warnings, fmt.Sprintf("plugin %q has an invalid name and is ignored", p.Name))
			continue
		}
		if _, dup := reg.Plugins[p.Name]; dup {
			reg.Warnings = append(reg.Warnings, fmt.Sprintf("plugin %q is listed twice; the second entry is ignored", p.Name))
			continue
		}
		if err := json.Unmarshal(p.Source, &source); err != nil {
			reg.Warnings = append(reg.Warnings, fmt.Sprintf("plugin %q is hosted outside the registry and is ignored", p.Name))
			continue
		}
		dir, err := pluginDir(m.Metadata.PluginRoot, source)
		if err != nil {
			reg.Warnings = append(reg.Warnings, fmt.Sprintf("plugin %q is ignored: %v", p.Name, err))
			continue
		}
		skills, err := discoverSkills(fsys, p.Name, dir)
		if err != nil {
			return nil, fmt.Errorf("plugin %q: %w", p.Name, err)
		}
		reg.checkPlugin(fsys, p.Name, dir, len(skills) > 0, p.Skills)
		for _, s := range skills {
			if err := skill.ValidateName(s.Name); err != nil {
				reg.Warnings = append(reg.Warnings, fmt.Sprintf("skill %s is ignored: %v", s.Dir, err))
				continue
			}
			reg.Skills = append(reg.Skills, s)
		}
		reg.Plugins[p.Name] = dir
	}
	sort.Slice(reg.Skills, func(i, j int) bool { return reg.Skills[i].ID < reg.Skills[j].ID })
	reg.checkUnlisted(fsys, listed, m.Metadata.PluginRoot)
	return reg, nil
}

// distributed explains what Skillcrew installs from a plugin.
const distributed = "Skillcrew only distributes skills/<name>/SKILL.md (or a SKILL.md at the plugin root)"

func (r *Registry) note(sev Severity, format string, args ...any) {
	r.layout = append(r.layout, Issue{Severity: sev, Message: fmt.Sprintf(format, args...)})
}

// checkPlugin reports listed plugins whose content Skillcrew cannot distribute.
func (r *Registry) checkPlugin(fsys fs.FS, name, dir string, hasSkills bool, inline json.RawMessage) {
	if info, err := fs.Stat(fsys, dir); err != nil || !info.IsDir() {
		r.note(Error, "plugin %q: source directory %s does not exist (fix its source in %s)", name, dir, MarketplaceFile)
		return
	}
	var manifest struct {
		Skills json.RawMessage `json:"skills"`
	}
	if data, err := fs.ReadFile(fsys, path.Join(dir, ".claude-plugin", "plugin.json")); err == nil {
		_ = json.Unmarshal(data, &manifest)
	}
	for _, declared := range []json.RawMessage{inline, manifest.Skills} {
		if customSkills(declared) {
			r.note(Warning, "plugin %q declares custom skill paths; %s", name, distributed)
			break
		}
	}
	if !hasSkills {
		r.note(Warning, "plugin %q has no skill (commands, agents, hooks and MCP servers are not distributed); %s", name, distributed)
	}
}

// customSkills reports whether a manifest "skills" field points anywhere but
// the default skills/ directory.
func customSkills(raw json.RawMessage) bool {
	if len(raw) == 0 || string(raw) == "null" {
		return false
	}
	var paths []string
	var one string
	if json.Unmarshal(raw, &one) == nil {
		paths = []string{one}
	} else if json.Unmarshal(raw, &paths) != nil {
		return true
	}
	for _, p := range paths {
		if path.Clean(p) != "skills" {
			return true
		}
	}
	return false
}

// checkUnlisted reports directories of the plugin roots that hold skills but
// are not listed in the marketplace: their skills are not distributed.
func (r *Registry) checkUnlisted(fsys fs.FS, listed map[string]bool, pluginRoot string) {
	roots := []string{"plugins"}
	if pluginRoot != "" {
		if root := path.Clean(pluginRoot); root != "plugins" && root != "." && !strings.HasPrefix(root, "..") && !path.IsAbs(root) {
			roots = append(roots, root)
		}
	}
	for _, root := range roots {
		entries, _ := fs.ReadDir(fsys, root)
		for _, e := range entries {
			dir := path.Join(root, e.Name())
			if !e.IsDir() || listed[dir] {
				continue
			}
			if skills, _ := discoverSkills(fsys, e.Name(), dir); len(skills) > 0 {
				r.note(Warning, "%s holds skills but is not listed in %s; its skills are not distributed", dir, MarketplaceFile)
			}
		}
	}
}

// ValidTagName reports whether name is a tag name git accepts
// (git check-ref-format), given without its refs/tags/ prefix. Names starting
// with a dash or refs/ are refused too: they would be read as options or as
// another ref.
func ValidTagName(name string) bool {
	if name == "" || name == "@" || strings.HasPrefix(name, "-") || strings.HasPrefix(name, "refs/") ||
		strings.HasPrefix(name, "/") || strings.HasSuffix(name, "/") || strings.HasSuffix(name, ".") ||
		strings.Contains(name, "..") || strings.Contains(name, "@{") || strings.Contains(name, "//") {
		return false
	}
	for _, c := range name {
		if c < 0x20 || c == 0x7f || strings.ContainsRune(" ~^:?*[\\", c) {
			return false
		}
	}
	for _, part := range strings.Split(name, "/") {
		if strings.HasPrefix(part, ".") || strings.HasSuffix(part, ".lock") {
			return false
		}
	}
	return true
}

// pluginName is the character set Claude Code accepts for plugin names.
var pluginName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// PluginDir returns the directory of a plugin.
func (r *Registry) PluginDir(name string) (string, bool) {
	dir, ok := r.Plugins[name]
	return dir, ok
}

func pluginDir(root, source string) (string, error) {
	if !strings.HasPrefix(source, "./") {
		source = path.Join(root, source)
	}
	dir := path.Clean(source)
	if dir == ".." || strings.HasPrefix(dir, "../") || path.IsAbs(dir) {
		return "", fmt.Errorf("source %q escapes the registry", source)
	}
	return dir, nil
}

func discoverSkills(fsys fs.FS, plugin, dir string) ([]Skill, error) {
	entries, err := fs.ReadDir(fsys, path.Join(dir, "skills"))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	var skills []Skill
	for _, e := range entries {
		skillDir := path.Join(dir, "skills", e.Name())
		if _, err := fs.Stat(fsys, path.Join(skillDir, skill.FileName)); e.IsDir() && err == nil {
			skills = append(skills, newSkill(plugin, skillDir))
		}
	}
	if len(skills) == 0 {
		if _, err := fs.Stat(fsys, path.Join(dir, skill.FileName)); err == nil {
			skills = append(skills, newSkill(plugin, dir))
		}
	}
	return skills, nil
}

func newSkill(plugin, dir string) Skill {
	name := path.Base(dir)
	return Skill{ID: plugin + ":" + name, Plugin: plugin, Name: name, Dir: dir}
}

// Skill returns the skill with the given ID.
func (r *Registry) Skill(id string) (Skill, bool) {
	i := sort.Search(len(r.Skills), func(i int) bool { return r.Skills[i].ID >= id })
	if i < len(r.Skills) && r.Skills[i].ID == id {
		return r.Skills[i], true
	}
	return Skill{}, false
}

// InstallName is the directory name a skill is installed under.
func (r *Registry) InstallName(s Skill) string {
	if alias := r.Governance.Skills[s.ID].InstallAs; alias != "" {
		return alias
	}
	return s.Name
}
