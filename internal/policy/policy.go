// Package policy resolves the governance of a registry into the set of skills
// a given developer must have.
package policy

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/yoanbernabeu/skillcrew/internal/registry"
)

// Prefs are the developer's choices.
type Prefs struct {
	Teams     []string
	Disabled  []string // recommended skills turned off
	Installed []string // available skills turned on
	Present   []string // skills currently installed on the machine
}

// Deprecation is the notice attached to a deprecated skill.
type Deprecation struct {
	Message    string
	ReplacedBy string
}

// Entry is the resolved state of one registry skill.
type Entry struct {
	Skill       registry.Skill
	InstallName string
	// Status is one of Available, Recommended, Deprecated, Required or
	// Blocked. Deprecation is also reported by a stricter status.
	Status      registry.Status
	Deprecation *Deprecation
	Install     bool
	Ref, SHA    string
}

// Plan is the resolution result, one entry per registry skill.
type Plan struct {
	Entries  []Entry
	Warnings []string
}

// Wanted returns the entries that must be installed.
func (p Plan) Wanted() []Entry {
	var out []Entry
	for _, e := range p.Entries {
		if e.Install {
			out = append(out, e)
		}
	}
	return out
}

var strictness = map[registry.Status]int{
	registry.Available:   0,
	registry.Recommended: 1,
	registry.Deprecated:  1,
	registry.Required:    2,
}

// Resolve computes the plan for a developer.
func Resolve(reg *registry.Registry, prefs Prefs) Plan {
	var plan Plan
	g := reg.Governance
	for _, team := range prefs.Teams {
		if _, ok := g.Teams[team]; !ok {
			plan.warn("team %q does not exist in the registry", team)
		}
	}
	for _, s := range reg.Skills {
		org := g.OrgRule(s.ID)
		e := Entry{Skill: s, InstallName: reg.InstallName(s), Ref: org.Ref, SHA: org.SHA}
		rules := []registry.Rule{org}
		for _, team := range prefs.Teams {
			if r, ok := g.Teams[team].Skills[s.ID]; ok {
				if org.Status == registry.Required && r.Status != registry.Required {
					plan.warn("team %q cannot soften %s, required by the organization", team, s.ID)
					continue
				}
				rules = append(rules, r)
			}
		}
		e.Status, e.Deprecation = merge(rules)
		e.Install = wanted(e.Status, s.ID, prefs)
		plan.Entries = append(plan.Entries, e)
	}
	plan.dropCollisions()
	return plan
}

// merge combines the organization rule (first) with team rules: an
// organization block always wins, then a requirement from any level, then a
// team block, then the strictest remaining status.
func merge(rules []registry.Rule) (registry.Status, *Deprecation) {
	if rules[0].Status == registry.Blocked {
		return registry.Blocked, nil
	}
	status, blocked := registry.Available, false
	var dep *Deprecation
	for _, r := range rules {
		blocked = blocked || r.Status == registry.Blocked
		if r.Status == registry.Deprecated && dep == nil {
			dep = &Deprecation{Message: r.Message, ReplacedBy: r.ReplacedBy}
		}
		if strictness[r.Status] > strictness[status] {
			status = r.Status
		}
	}
	switch {
	case status == registry.Required:
		return status, dep
	case blocked:
		return registry.Blocked, nil
	}
	return status, dep
}

func wanted(status registry.Status, id string, prefs Prefs) bool {
	switch status {
	case registry.Required:
		return true
	case registry.Recommended:
		return !slices.Contains(prefs.Disabled, id)
	case registry.Deprecated:
		// Stays where it is, never spreads to new machines.
		kept := slices.Contains(prefs.Present, id) || slices.Contains(prefs.Installed, id)
		return kept && !slices.Contains(prefs.Disabled, id)
	case registry.Available:
		return slices.Contains(prefs.Installed, id)
	}
	return false
}

func (p *Plan) dropCollisions() {
	byName := map[string][]int{}
	for i, e := range p.Entries {
		if e.Install {
			byName[e.InstallName] = append(byName[e.InstallName], i)
		}
	}
	names := make([]string, 0, len(byName))
	for name := range byName {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		idx := byName[name]
		if len(idx) < 2 {
			continue
		}
		ids := make([]string, len(idx))
		for k, i := range idx {
			p.Entries[i].Install = false
			ids[k] = p.Entries[i].Skill.ID
		}
		p.warn("skills %s collide on %q and are not installed; the registry must set install_as", strings.Join(ids, ", "), name)
	}
}

func (p *Plan) warn(format string, args ...any) {
	p.Warnings = append(p.Warnings, fmt.Sprintf(format, args...))
}
