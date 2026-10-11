package registry

import (
	"fmt"
	"io/fs"
	"maps"
	"regexp"
	"slices"
	"strings"

	"golang.org/x/text/unicode/norm"

	"github.com/yoanbernabeu/skillcrew/internal/skill"
)

// Severity of a validation issue.
type Severity string

// Issue severities: errors fail validation, warnings do not.
const (
	Error   Severity = "error"
	Warning Severity = "warning"
)

// Issue is a validation finding.
type Issue struct {
	Severity Severity `json:"severity"`
	Message  string   `json:"message"`
	Skill    string   `json:"skill,omitempty"` // id of the skill concerned, when there is one
}

func (i Issue) String() string { return string(i.Severity) + ": " + i.Message }

// Validate checks the skills and the governance of a registry loaded from fsys.
func (r *Registry) Validate(fsys fs.FS) []Issue {
	v := &validator{reg: r}
	for _, w := range r.Warnings {
		v.add(Warning, "%s", w)
	}
	v.issues = append(v.issues, r.layout...)
	for _, s := range r.Skills {
		v.skill = s.ID
		// A blocked skill is never installed: its content cannot harm.
		sev, note := Error, ""
		if v.blockedEverywhere(s.ID) {
			sev, note = Warning, " (blocked, so never installed)"
		}
		for _, err := range skill.Validate(fsys, s.Dir) {
			v.add(sev, "%s: %v%s", s.ID, err, note)
		}
		v.checkPaths(fsys, s)
	}
	v.skill = ""
	g := r.Governance
	if g.Org == "" {
		v.add(Error, "%s: org is required", GovernanceFile)
	}
	for _, id := range slices.Sorted(maps.Keys(g.Skills)) {
		v.checkOrgRule(id, g.Skills[id])
	}
	for _, team := range slices.Sorted(maps.Keys(g.Teams)) {
		rules := g.Teams[team].Skills
		for _, id := range slices.Sorted(maps.Keys(rules)) {
			v.checkTeamRule(team, id, rules[id])
		}
	}
	v.checkCollisions()
	return v.issues
}

type validator struct {
	reg    *Registry
	issues []Issue
	skill  string // skill id the next issues are about
}

func (v *validator) add(sev Severity, format string, args ...any) {
	v.issues = append(v.issues, Issue{Severity: sev, Message: fmt.Sprintf(format, args...), Skill: v.skill})
}

func (v *validator) checkKnown(where, id string, rule Rule) {
	if _, ok := v.reg.Skill(id); !ok && rule.Status != Blocked {
		v.add(Error, "%s: unknown skill %q", where, id)
	}
	if rule.ReplacedBy != "" {
		if _, ok := v.reg.Skill(rule.ReplacedBy); !ok {
			v.add(Error, "%s: %s is replaced by unknown skill %q", where, id, rule.ReplacedBy)
		}
	}
}

// commitID matches a full commit id (SHA-1 or SHA-256).
var commitID = regexp.MustCompile(`^([0-9a-f]{40}|[0-9a-f]{64})$`)

func (v *validator) checkOrgRule(id string, rule Rule) {
	v.skill = id
	defer func() { v.skill = "" }()
	v.checkKnown("skills", id, rule)
	if rule.Ref != "" && rule.SHA != "" {
		v.add(Error, "skills: %s sets both ref and sha", id)
	}
	if rule.SHA != "" && !commitID.MatchString(rule.SHA) {
		v.add(Error, "skills: %s sha %q must be a full commit id (40 or 64 lowercase hexadecimal characters)", id, rule.SHA)
	}
	if rule.Ref != "" && !ValidTagName(rule.Ref) {
		v.add(Error, "skills: %s ref %q must be a valid tag name, such as v1.2.0 (without refs/tags/)", id, rule.Ref)
	}
	if rule.InstallAs != "" {
		if err := skill.ValidateName(rule.InstallAs); err != nil {
			v.add(Error, "skills: %s install_as: %v", id, err)
		}
	}
}

func (v *validator) checkTeamRule(team, id string, rule Rule) {
	v.skill = id
	defer func() { v.skill = "" }()
	where := "teams." + team
	v.checkKnown(where, id, rule)
	if rule.Status == "" {
		v.add(Error, "%s: %s: status is required", where, id)
	}
	if v.reg.Governance.OrgRule(id).Status == Blocked && rule.Status != Blocked {
		v.add(Error, "%s: %s is blocked by the organization; a team cannot allow it", where, id)
	}
	if rule.Pinned() || rule.InstallAs != "" {
		v.add(Error, "%s: %s: ref, sha and install_as are allowed only at organization level", where, id)
	}
	if v.reg.Governance.OrgRule(id).Status == Required && rule.Status != Required {
		v.add(Error, "%s: %s is required by the organization; a team cannot soften it to %s", where, id, rule.Status)
	}
}

func (v *validator) checkCollisions() {
	owners := map[string][]string{}
	for _, s := range v.reg.Skills {
		if v.blockedEverywhere(s.ID) {
			continue
		}
		name := v.reg.InstallName(s)
		owners[name] = append(owners[name], s.ID)
	}
	for _, name := range slices.Sorted(maps.Keys(owners)) {
		if ids := owners[name]; len(ids) > 1 {
			v.add(Error, "skills %s are all installed as %q; set install_as on all but one", strings.Join(ids, ", "), name)
		}
	}
}

// blockedEverywhere reports whether no team can make the skill installable.
func (v *validator) blockedEverywhere(id string) bool {
	return v.reg.Governance.OrgRule(id).Status == Blocked
}

// checkPaths rejects files whose names differ only by case or Unicode
// normalization: macOS would install one in place of the other, so what is
// installed would differ from what Linux CI and reviewers see.
func (v *validator) checkPaths(fsys fs.FS, s Skill) {
	seen := map[string]string{}
	_ = fs.WalkDir(fsys, s.Dir, func(p string, _ fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		key := strings.ToLower(norm.NFC.String(p))
		if other, ok := seen[key]; ok {
			v.add(Error, "%s: %q and %q name the same file on macOS (case or Unicode normalization)", s.ID, other, p)
		}
		seen[key] = p
		return nil
	})
}
