package scaffold

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing/fstest"
	"unicode"
	"unicode/utf8"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
	"gopkg.in/yaml.v3"

	"github.com/yoanbernabeu/skillcrew/internal/registry"
	"github.com/yoanbernabeu/skillcrew/internal/skill"
)

// SkillOptions configures NewSkill.
type SkillOptions struct {
	ID          string // "plugin:skill"
	Description string // what the skill does and when to use it
	// Status, when set, is written to skillcrew.yaml: as an organization
	// rule, or as a rule of Team when Team is set.
	Status registry.Status
	Team   string
}

// SkillResult lists what NewSkill did.
type SkillResult struct {
	File     string   // the new SKILL.md, relative to dir
	Created  []string // files created, relative to dir, sorted
	Modified []string // files changed, relative to dir, sorted
	Notices  []string // e.g. a team created in skillcrew.yaml
	// OrgStatus is the status of the skill for the whole organization.
	OrgStatus registry.Status
	// RuleExists reports that skillcrew.yaml already had an organization
	// rule for the skill, which OrgStatus reflects.
	RuleExists bool
	// Issues are what "skillcrew validate" reports on the result and did not
	// report before, except the placeholder warning of the new skill.
	Issues []registry.Issue
}

// NewSkill adds a skill to the registry in dir: its SKILL.md, its plugin
// (plugin.json and marketplace entry) when the plugin is new, and its status
// in skillcrew.yaml when opts.Status is set. Everything is checked before
// anything is written, so a refusal leaves the registry untouched. Like
// Init, it never writes through a symbolic link.
func NewSkill(dir string, opts SkillOptions) (SkillResult, error) {
	plugin, name, err := checkSkillOptions(opts)
	if err != nil {
		return SkillResult{}, err
	}
	fsys := os.DirFS(dir)
	if _, err := fs.Stat(fsys, registry.MarketplaceFile); err != nil {
		return SkillResult{}, fmt.Errorf("%s is not a registry: %s not found (run \"skillcrew registry init\" to create one)", dir, registry.MarketplaceFile)
	}
	reg, err := registry.Load(fsys)
	if err != nil {
		return SkillResult{}, err
	}
	if s, ok := reg.Skill(opts.ID); ok {
		return SkillResult{}, fmt.Errorf("skill %s already exists in %s", opts.ID, s.Dir)
	}
	installName := name
	if alias := reg.Governance.Skills[opts.ID].InstallAs; alias != "" {
		installName = alias
	}
	for _, s := range reg.Skills {
		if reg.InstallName(s) == installName && reg.Governance.OrgRule(s.ID).Status != registry.Blocked {
			return SkillResult{}, fmt.Errorf("skill name %q is already used by %s (skills are installed under their bare name or install_as alias); choose another name", installName, s.ID)
		}
	}

	created, modified := map[string]string{}, map[string]string{}
	pluginDir, listed := reg.PluginDir(plugin)
	if listed {
		for _, s := range reg.Skills {
			if s.Plugin == plugin && s.Dir == pluginDir {
				return SkillResult{}, fmt.Errorf("plugin %q is a single skill with a SKILL.md at its root; adding skills/ would hide it: add the skill to another plugin", plugin)
			}
		}
	} else {
		if pluginDir, err = newPlugin(fsys, plugin, created, modified); err != nil {
			return SkillResult{}, err
		}
	}
	file := path.Join(pluginDir, "skills", name, skill.FileName)
	created[file] = skillFile(name, opts.Description)

	var notices []string
	if opts.Status != "" {
		gov, notice, err := governanceWithRule(fsys, reg, opts)
		if err != nil {
			return SkillResult{}, err
		}
		modified[registry.GovernanceFile] = gov
		notices = append(notices, notice...)
	}

	if err := checkDiscovered(fsys, created, modified, opts.ID, file); err != nil {
		return SkillResult{}, err
	}
	for _, rel := range slices.Sorted(maps.Keys(created)) {
		if _, err := checkPath(dir, rel); err != nil {
			return SkillResult{}, err
		}
	}
	for _, rel := range slices.Sorted(maps.Keys(modified)) {
		if _, err := checkPath(dir, rel); err != nil {
			return SkillResult{}, err
		}
	}
	if !listed {
		if _, err := os.Lstat(filepath.Join(dir, filepath.FromSlash(pluginDir))); err == nil {
			return SkillResult{}, fmt.Errorf("%s already exists (or a directory whose name differs only by case) but is not listed in %s; list it there, or choose another plugin name", pluginDir, registry.MarketplaceFile)
		}
	}
	skillDir := filepath.Join(dir, filepath.FromSlash(pluginDir), "skills", name)
	if _, err := os.Lstat(skillDir); err == nil {
		return SkillResult{}, fmt.Errorf("%s already exists; remove it or choose another name", path.Join(pluginDir, "skills", name))
	}

	var reported []string
	for _, i := range reg.Validate(fsys) {
		reported = append(reported, i.Message)
	}
	_, ruleExists := reg.Governance.Skills[opts.ID]
	orgStatus := reg.Governance.OrgRule(opts.ID).Status
	if opts.Status != "" && opts.Team == "" {
		orgStatus = opts.Status
	}
	files := maps.Clone(created)
	maps.Copy(files, modified)
	if err := writeFiles(dir, files); err != nil {
		return SkillResult{}, err
	}
	// The placeholder of the new skill is expected: the caller asks to
	// replace it.
	issues := slices.DeleteFunc(validate(dir, reported), func(i registry.Issue) bool {
		return i.Skill == opts.ID && i.Severity == registry.Warning && strings.Contains(i.Message, file+" still holds the TODO")
	})
	return SkillResult{
		File:       file,
		Created:    slices.Sorted(maps.Keys(created)),
		Modified:   slices.Sorted(maps.Keys(modified)),
		Notices:    notices,
		OrgStatus:  orgStatus,
		RuleExists: ruleExists && (opts.Status == "" || opts.Team != ""),
		Issues:     issues,
	}, nil
}

// checkDiscovered checks, before anything is written, that the registry
// will discover the new skill where it is created: a marketplace.json that
// Skillcrew reads differently from the JSON editor (e.g. a duplicate key)
// is refused.
func checkDiscovered(fsys fs.FS, created, modified map[string]string, id, file string) error {
	marketplace, ok := modified[registry.MarketplaceFile]
	if !ok {
		data, err := fs.ReadFile(fsys, registry.MarketplaceFile)
		if err != nil {
			return err
		}
		marketplace = string(data)
	}
	check := fstest.MapFS{registry.MarketplaceFile: {Data: []byte(marketplace)}}
	for rel, content := range created {
		check[rel] = &fstest.MapFile{Data: []byte(content)}
	}
	reg, err := registry.Discover(check)
	if err == nil {
		if s, ok := reg.Skill(id); ok && path.Join(s.Dir, skill.FileName) == file {
			return nil
		}
	}
	return fmt.Errorf("could not add %s safely (is a key of %s defined twice?); edit %s by hand", id, registry.MarketplaceFile, registry.MarketplaceFile)
}

// checkSkillOptions checks the options that do not depend on the registry
// and returns the plugin and skill names.
func checkSkillOptions(opts SkillOptions) (plugin, name string, err error) {
	parts := strings.Split(opts.ID, ":")
	if len(parts) != 2 {
		return "", "", fmt.Errorf("skill id %q must be plugin:skill, e.g. git:commit-message", opts.ID)
	}
	plugin, name = parts[0], parts[1]
	if !pluginPattern.MatchString(plugin) {
		return "", "", fmt.Errorf("plugin name %q must use letters, digits, dots, underscores and hyphens", plugin)
	}
	if err := skill.ValidateName(name); err != nil {
		return "", "", err
	}
	desc := strings.TrimSpace(opts.Description)
	switch {
	case desc == "":
		return "", "", errors.New("a description is required: say what the skill does and when to use it (agents read it to decide when to load the skill)")
	case utf8.RuneCountInString(desc) > skill.MaxDescriptionLength:
		return "", "", fmt.Errorf("the description must be at most %d characters", skill.MaxDescriptionLength)
	case strings.ContainsFunc(desc, invisible):
		return "", "", errors.New("the description must not contain control characters, line breaks or bidirectional formatting characters")
	}
	switch opts.Status {
	case "", registry.Available, registry.Recommended, registry.Required, registry.Deprecated, registry.Blocked:
	default:
		return "", "", fmt.Errorf("unknown status %q: use required, recommended, available, deprecated or blocked", opts.Status)
	}
	if opts.Team != "" {
		if opts.Status == "" {
			return "", "", errors.New("a team rule needs a status (e.g. required)")
		}
		if !teamPattern.MatchString(opts.Team) {
			return "", "", fmt.Errorf("team name %q must use letters, digits, dots, underscores and hyphens", opts.Team)
		}
	}
	return plugin, name, nil
}

// invisible reports characters that have no place in a one-line
// description: control characters (C0 and C1), line and paragraph
// separators, and bidirectional formatting characters, which can make text
// read differently from what it is.
func invisible(r rune) bool {
	return unicode.IsControl(r) || r == '\u2028' || r == '\u2029' || unicode.Is(unicode.Bidi_Control, r)
}

// teamPattern matches the team names NewSkill creates.
var teamPattern = pluginPattern

// newPlugin adds the plugin.json and marketplace entry of a new plugin to
// created and modified, and returns the plugin directory.
func newPlugin(fsys fs.FS, plugin string, created, modified map[string]string) (string, error) {
	if err := skill.ValidateName(plugin); err != nil {
		return "", fmt.Errorf("plugin name %q must use lowercase letters, digits and single hyphens", plugin)
	}
	data, err := fs.ReadFile(fsys, registry.MarketplaceFile)
	if err != nil {
		return "", err
	}
	doc := string(data)
	for _, n := range gjson.Get(doc, "plugins.#.name").Array() {
		if n.String() == plugin {
			return "", fmt.Errorf("plugin %q is listed in %s but Skillcrew ignores it (hosted outside the registry, or invalid: run \"skillcrew validate\"); add the skill to another plugin", plugin, registry.MarketplaceFile)
		}
	}
	if list := gjson.Get(doc, "plugins"); list.Exists() && !list.IsArray() {
		return "", fmt.Errorf("%s: plugins is not an array", registry.MarketplaceFile)
	}
	dir := "plugins/" + plugin
	if doc, err = appendPlugin(doc, plugin, "./"+dir); err != nil {
		return "", err
	}
	modified[registry.MarketplaceFile] = doc
	// No version: Claude Code would only update the plugin when it changes.
	created[dir+"/.claude-plugin/plugin.json"] = marshal(map[string]string{"name": plugin})
	return dir, nil
}

// appendPlugin adds {"name": name, "source": source} at the end of the
// plugins array of a marketplace, in the layout of the last entry, so only
// the new lines change.
func appendPlugin(doc, name, source string) (string, error) {
	type entry struct {
		Name   string `json:"name"`
		Source string `json:"source"`
	}
	n := gjson.Get(doc, "plugins.#").Int()
	last := gjson.Get(doc, fmt.Sprintf("plugins.%d", n-1))
	if n == 0 || last.Index <= 0 {
		compact, err := json.Marshal(entry{name, source})
		if err != nil {
			return "", err
		}
		return sjson.SetRaw(doc, "plugins.-1", string(compact))
	}
	eol := "\n"
	if strings.Contains(doc, "\r\n") {
		eol = "\r\n"
	}
	key := func(s string) string { b, _ := json.Marshal(s); return string(b) }
	oneLine := `{"name":` + key(name) + `,"source":` + key(source) + `}`
	if strings.Contains(last.Raw, `": `) {
		oneLine = `{"name": ` + key(name) + `, "source": ` + key(source) + `}`
	}
	end := last.Index + len(last.Raw)
	indent := doc[strings.LastIndexByte(doc[:last.Index], '\n')+1 : last.Index]
	var add string
	switch lines := strings.Split(strings.ReplaceAll(last.Raw, "\r\n", "\n"), "\n"); {
	case strings.TrimSpace(indent) != "": // entries share a line
		add = ", " + oneLine
	case len(lines) > 1: // one entry per line, over several lines
		inner := lines[1][:len(lines[1])-len(strings.TrimLeft(lines[1], " \t"))]
		unit := strings.TrimPrefix(inner, indent)
		if unit == inner || unit == "" {
			unit = "  "
		}
		block, err := json.MarshalIndent(entry{name, source}, indent, unit)
		if err != nil {
			return "", err
		}
		add = "," + eol + indent + strings.ReplaceAll(string(block), "\n", eol)
	default: // one entry per line
		add = "," + eol + indent + oneLine
	}
	return doc[:end] + add + doc[end:], nil
}

// skillFile renders a new SKILL.md.
func skillFile(name, description string) string {
	fm, err := yaml.Marshal(skill.Frontmatter{Name: name, Description: strings.TrimSpace(description)})
	if err != nil {
		panic(err) // two strings always marshal
	}
	title := strings.ReplaceAll(name, "-", " ")
	title = strings.ToUpper(title[:1]) + title[1:]
	return "---\n" + string(fm) + "---\n\n# " + title + "\n\n" + skill.Placeholder + "\n" + skillBody
}

const skillBody = `Keep them short and specific. Put long reference material in other files of
this directory and say when to read them.
`

// governanceWithRule returns skillcrew.yaml with the rule of opts added,
// and notices about what else was created.
func governanceWithRule(fsys fs.FS, reg *registry.Registry, opts SkillOptions) (string, []string, error) {
	path := []string{"skills"}
	if opts.Team != "" {
		path = []string{"teams", opts.Team, "skills"}
		org := reg.Governance.OrgRule(opts.ID).Status
		if org == registry.Blocked && opts.Status != registry.Blocked {
			return "", nil, fmt.Errorf("%s is blocked by the organization; a team cannot allow it", opts.ID)
		}
		if org == registry.Required && opts.Status != registry.Required {
			return "", nil, fmt.Errorf("%s is required by the organization; a team cannot soften it to %s", opts.ID, opts.Status)
		}
	}
	data, err := fs.ReadFile(fsys, registry.GovernanceFile)
	if err != nil {
		return "", nil, err
	}
	out, created, err := addRule(data, path, opts.ID, opts.Status)
	if err != nil {
		return "", nil, err
	}
	// Check the edit: the new file must hold the rule, and nothing else may
	// have changed.
	marketplace, err := fs.ReadFile(fsys, registry.MarketplaceFile)
	if err != nil {
		return "", nil, err
	}
	check, err := registry.Load(fstest.MapFS{
		registry.MarketplaceFile: {Data: marketplace},
		registry.GovernanceFile:  {Data: out},
	})
	if err != nil || !onlyAdded(reg.Governance, check.Governance, opts) {
		return "", nil, fmt.Errorf("could not add %s to %s safely; edit skillcrew.yaml by hand", opts.ID, registry.GovernanceFile)
	}
	var notices []string
	if opts.Team != "" && slices.Contains(created, opts.Team) {
		notices = append(notices, fmt.Sprintf("team %q created in %s", opts.Team, registry.GovernanceFile))
	}
	return string(out), notices, nil
}

// onlyAdded reports whether after is before plus the rule of opts.
func onlyAdded(before, after registry.Governance, opts SkillOptions) bool {
	rules := after.Skills
	if opts.Team != "" {
		rules = after.Teams[opts.Team].Skills
	}
	if rules[opts.ID] != (registry.Rule{Status: opts.Status}) {
		return false
	}
	delete(rules, opts.ID)
	if opts.Team != "" && len(rules) == 0 {
		if _, existed := before.Teams[opts.Team]; !existed {
			delete(after.Teams, opts.Team)
		}
	}
	return reflect.DeepEqual(normalize(before), normalize(after))
}

// normalize turns empty maps into nil maps, which parse the same.
func normalize(g registry.Governance) registry.Governance {
	if len(g.Skills) == 0 {
		g.Skills = nil
	}
	teams := map[string]registry.Team{}
	for name, t := range g.Teams {
		if len(t.Skills) == 0 {
			t.Skills = nil
		}
		teams[name] = t
	}
	g.Teams = nil
	if len(teams) > 0 {
		g.Teams = teams
	}
	return g
}
