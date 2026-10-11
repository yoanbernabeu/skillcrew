// Package scaffold creates Skillcrew registries ("skillcrew registry init"):
// it either adds a skillcrew.yaml to an existing Claude Code marketplace or
// scaffolds a new marketplace with an example plugin and skill.
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
	"regexp"
	"slices"
	"strings"
	"syscall"

	"gopkg.in/yaml.v3"

	"github.com/yoanbernabeu/skillcrew/internal/registry"
	"github.com/yoanbernabeu/skillcrew/internal/skill"
)

// Options configures Init.
type Options struct {
	Org   string // organization name; defaults to the marketplace name, else a sanitized dir basename
	Owner string // marketplace owner name (used only when creating a marketplace); default = Org
	Force bool   // overwrite an existing skillcrew.yaml
	// Version is the running skillcrew version. When it is a release
	// (vX.Y.Z), the generated CI workflow pins it; otherwise it uses @latest.
	Version string
}

// Result lists what Init did.
type Result struct {
	Converted bool     // an existing Claude Code marketplace was converted
	Created   []string // files created, relative to dir, sorted
	Notices   []string // how the organization name was chosen
	Warnings  []string // e.g. external plugins ignored, skills aliased or left out
	// Issues are what "skillcrew validate" reports on the result, minus the
	// warnings already listed.
	Issues []registry.Issue
}

// releasePattern matches a release version that the generated workflow can pin.
var releasePattern = regexp.MustCompile(`^v\d+\.\d+\.\d+$`)

// pluginPattern matches the plugin names emitted into skillcrew.yaml.
var pluginPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// Init turns dir into a Skillcrew registry. If dir holds a Claude Code
// marketplace, only skillcrew.yaml is written, listing every skill as
// available. Otherwise a new marketplace is scaffolded; no existing file is
// ever overwritten in that case. Init never writes through a symbolic link:
// it fails if any existing path it would write to, or any directory on the
// way, is one.
func Init(dir string, opts Options) (Result, error) {
	exists, err := checkPath(dir, registry.GovernanceFile)
	if err != nil {
		return Result{}, err
	}
	if exists && !opts.Force {
		return Result{}, fmt.Errorf("%s already exists (use --force to overwrite it)", registry.GovernanceFile)
	}
	if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(registry.MarketplaceFile))); err == nil {
		return convert(dir, opts)
	}
	return create(dir, opts)
}

func convert(dir string, opts Options) (Result, error) {
	fsys := os.DirFS(dir)
	reg, err := registry.Discover(fsys)
	if err != nil {
		return Result{}, err
	}
	org, notices := orgName(opts.Org, reg.Name, "the marketplace name", dir)
	rules := map[string]rule{}
	warnings := append([]string(nil), reg.Warnings...)
	var valid []registry.Skill
	for _, s := range reg.Skills {
		if !pluginPattern.MatchString(s.Plugin) || skill.ValidateName(s.Name) != nil {
			warnings = append(warnings, fmt.Sprintf("skill %q ignored: invalid plugin or skill name", s.ID))
			continue
		}
		if errs := skill.Validate(fsys, s.Dir); len(errs) > 0 {
			reason := oneLine(errors.Join(errs...).Error())
			rules[s.ID] = rule{status: registry.Blocked, comment: "fails validation: " + reason + "; fix it, then change its status"}
			warnings = append(warnings, fmt.Sprintf("skill %s fails validation and is left out (blocked in %s): %s", s.ID, registry.GovernanceFile, reason))
			continue
		}
		rules[s.ID] = rule{status: registry.Available}
		valid = append(valid, s)
	}
	warnings = append(warnings, aliasCollisions(valid, rules)...)
	files := map[string]string{registry.GovernanceFile: governance(org, rules)}
	if err := writeFiles(dir, files); err != nil {
		return Result{}, err
	}
	return Result{
		Converted: true, Created: []string{registry.GovernanceFile}, Notices: notices,
		Warnings: warnings, Issues: validate(dir, warnings),
	}, nil
}

// orgName picks the organization name: the --org option, else fallback (the
// marketplace name), else the directory name. It explains its choice.
func orgName(option, fallback, fallbackWhat, dir string) (string, []string) {
	var notices []string
	name, from := option, ""
	switch {
	case option != "":
	case fallback != "":
		name, from = fallback, fallbackWhat
	default:
		name, from = baseName(dir), "the directory name"
	}
	org := SanitizeOrg(name)
	if from != "" {
		notices = append(notices, fmt.Sprintf("organization name %s taken from %s; use --org to choose another", org, from))
	}
	if org != name {
		notices = append(notices, fmt.Sprintf("%q was turned into %s: organization names use lowercase letters, digits and dashes", name, org))
	}
	return org, notices
}

// aliasCollisions gives every skill whose bare name is shared an install_as
// alias "<plugin>-<name>", so the registry installs them all.
func aliasCollisions(skills []registry.Skill, rules map[string]rule) []string {
	byName := map[string][]registry.Skill{}
	for _, s := range skills {
		byName[s.Name] = append(byName[s.Name], s)
	}
	taken := map[string]bool{}
	for name, owners := range byName {
		if len(owners) == 1 {
			taken[name] = true
		}
	}
	var warnings []string
	for _, name := range slices.Sorted(maps.Keys(byName)) {
		owners := byName[name]
		if len(owners) < 2 {
			continue
		}
		var given []string
		for _, s := range owners {
			alias := uniqueAlias(s.Plugin+"-"+s.Name, taken)
			taken[alias] = true
			r := rules[s.ID]
			r.installAs = alias
			rules[s.ID] = r
			given = append(given, s.ID+" as "+alias)
		}
		warnings = append(warnings, fmt.Sprintf("skills named %q collide; install_as installs %s", name, strings.Join(given, ", ")))
	}
	return warnings
}

// uniqueAlias turns base into a valid skill name not in taken.
func uniqueAlias(base string, taken map[string]bool) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(base) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			if dash && b.Len() > 0 {
				b.WriteByte('-')
			}
			b.WriteRune(r)
			dash = false
		} else {
			dash = true
		}
	}
	slug := b.String()
	if len(slug) > 58 {
		slug = strings.TrimRight(slug[:58], "-")
	}
	if slug == "" {
		slug = "skill"
	}
	alias := slug
	for i := 2; taken[alias] || skill.ValidateName(alias) != nil; i++ {
		alias = fmt.Sprintf("%s-%d", slug, i)
	}
	return alias
}

// validate runs "skillcrew validate" on dir, leaving out the warnings
// already reported.
func validate(dir string, reported []string) []registry.Issue {
	fsys := os.DirFS(dir)
	reg, err := registry.Load(fsys)
	if err != nil {
		return []registry.Issue{{Severity: registry.Error, Message: err.Error()}}
	}
	var issues []registry.Issue
	for _, i := range reg.Validate(fsys) {
		if !slices.Contains(reported, i.Message) {
			issues = append(issues, i)
		}
	}
	return issues
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

func create(dir string, opts Options) (Result, error) {
	org, notices := orgName(opts.Org, "", "", dir)
	owner := opts.Owner
	if owner == "" {
		owner = org
	}
	files := map[string]string{
		registry.MarketplaceFile: marshal(map[string]any{
			"name":  org,
			"owner": map[string]string{"name": owner},
			"plugins": []map[string]string{{
				"name":        "example",
				"source":      "./plugins/example",
				"description": exampleDescription,
			}},
		}),
		"plugins/example/.claude-plugin/plugin.json": marshal(map[string]string{
			"name":        "example",
			"description": exampleDescription,
			"version":     "0.1.0",
		}),
		"plugins/example/skills/hello-world/SKILL.md": helloWorldSkill,
		registry.GovernanceFile:                       governance(org, map[string]rule{"example:hello-world": {status: registry.Recommended}}),
		"README.md":                                   fmt.Sprintf(readmeTemplate, org),
		"AGENTS.md":                                   fmt.Sprintf(agentsTemplate, org),
		"CLAUDE.md":                                   "@AGENTS.md\n",
		".github/workflows/validate.yml":              workflow(opts.Version),
	}
	// Instructions for AI agents may already be there: keep them.
	for _, kept := range []struct{ name, notice string }{
		{"AGENTS.md", "AGENTS.md already exists and is kept; add the rules of the registry for AI agents to it (see https://skillcrew.yoandev.co/docs/getting-started/create-a-registry/)"},
		{"CLAUDE.md", `CLAUDE.md already exists and is kept; add a line "@AGENTS.md" to it so Claude Code reads the rules of the registry`},
	} {
		exists, err := checkPath(dir, kept.name)
		if err != nil {
			return Result{}, err
		}
		if exists {
			delete(files, kept.name)
			notices = append(notices, kept.notice)
		}
	}
	names := slices.Sorted(maps.Keys(files))
	for _, name := range names {
		if name == registry.GovernanceFile {
			continue // already checked by Init, honoring Force
		}
		exists, err := checkPath(dir, name)
		if err != nil {
			return Result{}, err
		}
		if exists {
			return Result{}, fmt.Errorf("%s already exists; refusing to overwrite it (run \"skillcrew registry init\" in an empty directory, or in a Claude Code marketplace to convert it)", name)
		}
	}
	if err := writeFiles(dir, files); err != nil {
		return Result{}, err
	}
	return Result{Created: names, Notices: notices, Issues: validate(dir, nil)}, nil
}

// writeFiles writes files (slash-separated paths relative to dir) with mode
// 0644. New files are created exclusively; an existing regular file is
// replaced atomically by renaming a temporary file over it.
func writeFiles(dir string, files map[string]string) error {
	for _, name := range slices.Sorted(maps.Keys(files)) {
		exists, err := checkPath(dir, name)
		if err != nil {
			return err
		}
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		if exists {
			err = replaceFile(p, files[name])
		} else {
			err = createFile(p, files[name])
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// checkPath reports whether rel (slash-separated, relative to dir) exists.
// It fails if an existing component of rel is a symbolic link, if an
// intermediate component is not a directory, or if rel itself exists but is
// not a regular file.
func checkPath(dir, rel string) (bool, error) {
	parts := strings.Split(rel, "/")
	p := dir
	for i, part := range parts {
		p = filepath.Join(p, part)
		info, err := os.Lstat(p)
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		name, last := path.Join(parts[:i+1]...), i == len(parts)-1
		switch {
		case info.Mode()&fs.ModeSymlink != 0:
			return false, fmt.Errorf("%s is a symbolic link; refusing to write through it", name)
		case !last && !info.IsDir():
			return false, fmt.Errorf("%s is not a directory", name)
		case last && !info.Mode().IsRegular():
			return false, fmt.Errorf("%s is not a regular file; refusing to overwrite it", name)
		}
	}
	return true, nil
}

// createFile creates p, failing if anything (even a dangling symlink) is
// already there.
func createFile(p, content string) error {
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0o644)
	if err != nil {
		return err
	}
	_, err = f.WriteString(content)
	return errors.Join(err, f.Close())
}

// replaceFile atomically replaces p with a new 0644 file holding content.
func replaceFile(p, content string) (err error) {
	f, err := os.CreateTemp(filepath.Dir(p), ".skillcrew-*.tmp")
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = os.Remove(f.Name())
		}
	}()
	_, err = f.WriteString(content)
	err = errors.Join(err, f.Chmod(0o644), f.Close())
	if err != nil {
		return err
	}
	return os.Rename(f.Name(), p)
}

// reservedNames are marketplace names Claude Code keeps for itself.
var reservedNames = []string{"claude", "anthropic", "claude-code"}

// SanitizeOrg turns name into a valid organization and marketplace name:
// lowercase letters, digits and dashes, never a name reserved by Claude Code.
func SanitizeOrg(name string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(name) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			if dash && b.Len() > 0 {
				b.WriteByte('-')
			}
			b.WriteRune(r)
			dash = false
		} else {
			dash = true
		}
	}
	s := b.String()
	if s == "" {
		return "my-org"
	}
	reserved := strings.HasPrefix(s, "claude-") || strings.HasPrefix(s, "anthropic-")
	for _, n := range reservedNames {
		reserved = reserved || s == n
	}
	if reserved {
		return "team-" + s
	}
	return s
}

func baseName(dir string) string {
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	return filepath.Base(dir)
}

func marshal(v any) string {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		panic(err) // only static, marshalable values are passed
	}
	return string(data) + "\n"
}

// rule is a skill entry of the generated skillcrew.yaml.
type rule struct {
	status    registry.Status
	installAs string
	comment   string // written above the entry
}

// governance renders skillcrew.yaml with explanatory comments.
func governance(org string, skills map[string]rule) string {
	var b strings.Builder
	fmt.Fprintf(&b, governanceHeader, quote(org))
	for _, id := range slices.Sorted(maps.Keys(skills)) {
		r := skills[id]
		if r.comment != "" {
			fmt.Fprintf(&b, "  # %s %s\n", id, strings.Map(func(c rune) rune {
				if c < 0x20 || c == 0x7f {
					return ' '
				}
				return c
			}, r.comment))
		}
		if r.installAs == "" {
			fmt.Fprintf(&b, "  %s: %s\n", quote(id), r.status)
			continue
		}
		fmt.Fprintf(&b, "  %s:\n    status: %s\n    install_as: %s\n", quote(id), r.status, quote(r.installAs))
	}
	b.WriteString(governanceFooter)
	return b.String()
}

// quote returns s as a plain YAML scalar when it is safe, else double-quoted.
func quote(s string) string {
	safe := s != ""
	for _, r := range s {
		safe = safe && (r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("-_.:", r))
	}
	if safe && !strings.HasSuffix(s, ":") && !strings.HasPrefix(s, "-") {
		// Words such as null, true or 1 would not read as strings.
		var v any
		if yaml.Unmarshal([]byte(s), &v) == nil && v == s {
			return s
		}
	}
	data, _ := json.Marshal(s) // JSON strings are valid YAML double-quoted scalars
	return string(data)
}

const exampleDescription = "Example plugin: replace it with your organization's skills."

const governanceHeader = `# Skillcrew governance file: https://github.com/yoanbernabeu/skillcrew
#
# Skill ids use the Claude Code notation "plugin:skill", e.g. security:review
# is the skill plugins/security/skills/review/SKILL.md of the "security" plugin.
#
# Statuses:
#   required     installed automatically and kept up to date; cannot be disabled
#   recommended  installed by default; developers can disable it
#   available    listed in the catalog, installed on demand (skillcrew install)
#   deprecated   stays where it is already installed (developers can disable
#                it) and is never installed on new machines; shown with a
#                message and a replacement:
#                  old:skill:
#                    status: deprecated
#                    message: "Replaced by new:skill"
#                    replaced_by: new:skill
#   blocked      forbidden: removed if present, never installed (kill switch)

org: %s

# Status of the skills not listed below.
default: available

skills:
`

const governanceFooter = `
# Teams can harden a status (e.g. make a skill required), never soften it.
# teams:
#   backend:
#     skills:
#       plugin:skill: required
`

const helloWorldSkill = `---
name: hello-world
description: Example skill showing the SKILL.md format. Use it when the user asks how skills work in this registry or how to write a new one.
---

# Hello world

This skill is an example shipped by ` + "`skillcrew registry init`" + `.

When it is used:

1. Explain that a skill is a folder under ` + "`plugins/<plugin>/skills/<name>/`" + ` with a ` + "`SKILL.md`" + ` file.
2. Point out that the frontmatter needs a ` + "`name`" + ` (equal to the folder name, lowercase letters, digits and dashes) and a ` + "`description`" + ` saying what the skill does and when to use it.
3. Suggest opening a pull request on the registry to add or change a skill.

Replace this skill with a real one once your registry is set up.
`

const readmeTemplate = `# Skill registry: %s

This repository is the skill registry of our organization, managed with
[Skillcrew](https://github.com/yoanbernabeu/skillcrew). It is also a regular
Claude Code marketplace.

## Join

Install Skillcrew, then run:

` + "```sh" + `
skillcrew init <this repository URL>
` + "```" + `

Skillcrew installs the skills of the organization and of your teams, and keeps
them up to date.

## Add a skill

` + "```sh" + `
skillcrew skill new <plugin>:<skill> --description "What it does and when to use it." --status recommended
` + "```" + `

The command creates the ` + "`SKILL.md`" + `, creates and lists the plugin when it
is new, and sets the status in ` + "`skillcrew.yaml`" + ` (` + "`--team <team>`" + ` for a
team rule). Write the instructions in the generated ` + "`SKILL.md`" + `, then open a
pull request: CI runs ` + "`skillcrew validate`" + `. ` + "`AGENTS.md`" + ` gives AI agents the
same rules.

## Statuses

| Status | Behavior |
|---|---|
| ` + "`required`" + ` | Installed automatically, cannot be disabled. |
| ` + "`recommended`" + ` | Installed by default, can be disabled. |
| ` + "`available`" + ` | Installed on demand (` + "`skillcrew install`" + `). |
| ` + "`deprecated`" + ` | Stays where installed, never installed on new machines, can be disabled. |
| ` + "`blocked`" + ` | Removed and never installed. |

Skills not listed in ` + "`skillcrew.yaml`" + ` get the ` + "`default`" + ` status.
`

// agentsTemplate is the AGENTS.md of a new registry: the rules an AI agent
// follows when it changes the registry. %s is the organization.
const agentsTemplate = `# AGENTS.md

This repository is the skill registry of the %s organization, managed with
Skillcrew (https://github.com/yoanbernabeu/skillcrew). Every skill merged here
reaches the machine of every developer, in every AI coding agent: treat changes
like production code.

## Layout

- ` + "`plugins/<plugin>/skills/<skill>/SKILL.md`" + `: one skill; its id is ` + "`<plugin>:<skill>`" + `.
- ` + "`.claude-plugin/marketplace.json`" + `: lists the plugins (the repository is also a
  Claude Code marketplace).
- ` + "`skillcrew.yaml`" + `: governance, the status of each skill for the organization and
  for each team.

## Add a skill

1. Create it with the CLI, never by hand:

   ` + "```sh" + `
   skillcrew skill new <plugin>:<skill> --description "<what it does and when to use it>" [--status <status>] [--team <team>]
   ` + "```" + `

   It creates the ` + "`SKILL.md`" + `, creates the plugin and lists it in
   ` + "`.claude-plugin/marketplace.json`" + ` when it is new, and sets the status in
   ` + "`skillcrew.yaml`" + `. Ask which status and team to use if you were not told.
2. Write the instructions in the generated ` + "`SKILL.md`" + `, replacing the TODO. If you
   have a skill for writing skills, use it.
3. Run ` + "`skillcrew validate`" + ` and fix every error.

## Change a skill

- Edit its ` + "`SKILL.md`" + ` and files. Keep the frontmatter ` + "`name`" + ` equal to the
  directory name, and a ` + "`description`" + ` that says what the skill does and when to use it.
- Never rename or move a skill directory: its id would change. Create the new
  skill, then mark the old one ` + "`deprecated`" + ` with ` + "`message`" + ` and ` + "`replaced_by`" + `.

## Statuses

Set in ` + "`skillcrew.yaml`" + `, under ` + "`skills`" + ` (organization) or ` + "`teams.<team>.skills`" + `:

- ` + "`required`" + `: installed for everyone concerned, cannot be disabled.
- ` + "`recommended`" + `: installed by default, can be disabled.
- ` + "`available`" + `: installed on demand. Skills not listed get the ` + "`default`" + ` status.
- ` + "`deprecated`" + `: kept where installed, never installed on new machines.
- ` + "`blocked`" + `: removed from every machine at the next sync.

A team can harden an organization status, never soften it, and cannot allow a
skill the organization blocks.

## Rules

- Do not edit ` + "`.claude-plugin/marketplace.json`" + ` to add a skill: ` + "`skillcrew skill new`" + ` does it.
- Skill names use lowercase letters, digits and single hyphens, and must be
  unique across plugins: skills are installed under their bare name.
- Only skills are distributed: commands, agents, hooks and MCP servers of a
  plugin are ignored.
- Scripts bundled with a skill run on developer machines when agents use the
  skill: keep them small and easy to review.
- Run ` + "`skillcrew validate`" + ` before every commit; CI runs it on every pull request.
`

// workflow returns the registry CI workflow, pinning skillcrew to version
// when it is a release.
func workflow(version string) string {
	ref := "latest"
	if releasePattern.MatchString(version) {
		ref = version
	}
	return fmt.Sprintf(validateWorkflow, ref)
}

// validateWorkflow is the registry CI workflow; %s is the skillcrew version.
// Actions are pinned to commit SHAs; Dependabot can keep them up to date.
const validateWorkflow = `name: Validate skills

on:
  pull_request:
  push:
    branches: [main]

permissions:
  contents: read

jobs:
  validate:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1
        with:
          persist-credentials: false
          fetch-depth: 0 # tags and history, to check pinned skills
      - uses: actions/setup-go@b7ad1dad31e06c5925ef5d2fc7ad053ef454303e # v7.0.0
        with:
          go-version: 'stable'
      - run: go run github.com/yoanbernabeu/skillcrew/cmd/skillcrew@%s validate
`
