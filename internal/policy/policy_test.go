package policy_test

import (
	"strings"
	"testing"
	"testing/fstest"

	"github.com/yoanbernabeu/skillcrew/internal/policy"
	"github.com/yoanbernabeu/skillcrew/internal/registry"
)

func load(t *testing.T, governance string, skills ...string) *registry.Registry {
	t.Helper()
	fsys := fstest.MapFS{
		".claude-plugin/marketplace.json": {Data: []byte(`{"name":"acme","plugins":[{"name":"p","source":"./p"},{"name":"q","source":"./q"}]}`)},
		"skillcrew.yaml":                  {Data: []byte(governance)},
	}
	for _, id := range skills {
		plugin, name, _ := strings.Cut(id, ":")
		fsys[plugin+"/skills/"+name+"/SKILL.md"] = &fstest.MapFile{Data: []byte("---\nname: " + name + "\ndescription: d\n---\n")}
	}
	reg, err := registry.Load(fsys)
	if err != nil {
		t.Fatal(err)
	}
	return reg
}

func entry(t *testing.T, plan policy.Plan, id string) policy.Entry {
	t.Helper()
	for _, e := range plan.Entries {
		if e.Skill.ID == id {
			return e
		}
	}
	t.Fatalf("no entry for %s", id)
	return policy.Entry{}
}

func TestResolveOrgLevel(t *testing.T) {
	reg := load(t, `
org: acme
default: recommended
skills:
  p:req: required
  p:avail: available
  p:blocked: blocked
  p:old: {status: deprecated, message: Use p:req., replaced_by: p:req}
  p:pinned: {status: required, sha: abc123}
`, "p:req", "p:avail", "p:blocked", "p:old", "p:rec", "p:pinned")

	plan := policy.Resolve(reg, policy.Prefs{})
	cases := []struct {
		id      string
		status  registry.Status
		install bool
	}{
		{"p:req", registry.Required, true},
		{"p:avail", registry.Available, false},
		{"p:blocked", registry.Blocked, false},
		{"p:old", registry.Deprecated, false}, // never installed by deprecation
		{"p:rec", registry.Recommended, true}, // default status
		{"p:pinned", registry.Required, true},
	}
	for _, c := range cases {
		e := entry(t, plan, c.id)
		if e.Status != c.status || e.Install != c.install {
			t.Errorf("%s: got status=%s install=%v, want %s %v", c.id, e.Status, e.Install, c.status, c.install)
		}
	}
	old := entry(t, plan, "p:old")
	if old.Deprecation == nil || old.Deprecation.Message != "Use p:req." || old.Deprecation.ReplacedBy != "p:req" {
		t.Fatalf("missing deprecation notice: %+v", old.Deprecation)
	}
	if e := entry(t, plan, "p:pinned"); e.SHA != "abc123" {
		t.Fatalf("pin not propagated: %+v", e)
	}
}

func TestResolveUserPrefs(t *testing.T) {
	reg := load(t, `
org: acme
skills:
  p:req: required
  p:rec: recommended
  p:avail: available
  p:blocked: blocked
`, "p:req", "p:rec", "p:avail", "p:blocked")

	plan := policy.Resolve(reg, policy.Prefs{
		Disabled:  []string{"p:req", "p:rec"},
		Installed: []string{"p:avail", "p:blocked"},
	})
	want := map[string]bool{"p:req": true, "p:rec": false, "p:avail": true, "p:blocked": false}
	for id, install := range want {
		if e := entry(t, plan, id); e.Install != install {
			t.Errorf("%s: install=%v, want %v", id, e.Install, install)
		}
	}
}

func TestResolveTeams(t *testing.T) {
	reg := load(t, `
org: acme
skills:
  p:a: recommended
  p:b: required
  p:c: available
  p:d: available
teams:
  backend:
    skills:
      p:a: required
      p:b: blocked
      p:c: recommended
  data:
    skills:
      p:c: required
      p:d: blocked
  other:
    skills:
      p:a: blocked
`, "p:a", "p:b", "p:c", "p:d")

	plan := policy.Resolve(reg, policy.Prefs{Teams: []string{"backend", "data", "ghost"}})
	cases := map[string]registry.Status{
		"p:a": registry.Required, // team hardens; "other" is not joined
		"p:b": registry.Required, // a team cannot block an org-required skill
		"p:c": registry.Required, // the strictest team wins
		"p:d": registry.Blocked,
	}
	for id, status := range cases {
		if e := entry(t, plan, id); e.Status != status {
			t.Errorf("%s: status=%s, want %s", id, e.Status, status)
		}
	}
	warnings := strings.Join(plan.Warnings, "\n")
	for _, w := range []string{"ghost", "p:b"} {
		if !strings.Contains(warnings, w) {
			t.Errorf("expected a warning about %s, got:\n%s", w, warnings)
		}
	}
}

func TestResolveCollisions(t *testing.T) {
	reg := load(t, "org: acme\ndefault: recommended\n", "p:review", "q:review", "p:solo")
	plan := policy.Resolve(reg, policy.Prefs{})
	for _, id := range []string{"p:review", "q:review"} {
		if e := entry(t, plan, id); e.Install {
			t.Errorf("%s: colliding skills must not be installed", id)
		}
	}
	if e := entry(t, plan, "p:solo"); !e.Install || e.InstallName != "solo" {
		t.Errorf("unexpected entry: %+v", e)
	}
	if !strings.Contains(strings.Join(plan.Warnings, "\n"), "review") {
		t.Errorf("expected a collision warning, got %v", plan.Warnings)
	}
}

// "If at least one of my teams requires a skill, I have it":
// only the organization can block a skill another team requires.
func TestTeamRequirementBeatsAnotherTeamBlock(t *testing.T) {
	reg := load(t, `
org: acme
skills:
  p:a: available
  p:b: blocked
teams:
  t1:
    skills:
      p:a: required
      p:b: required
  t2:
    skills:
      p:a: blocked
`, "p:a", "p:b")
	plan := policy.Resolve(reg, policy.Prefs{Teams: []string{"t1", "t2"}})
	if e := entry(t, plan, "p:a"); e.Status != registry.Required || !e.Install {
		t.Errorf("p:a: %+v", e)
	}
	if e := entry(t, plan, "p:b"); e.Status != registry.Blocked || e.Install {
		t.Errorf("an organization block always wins: %+v", e)
	}
}

// A deprecated skill stays where it is installed (or opted in) and is never
// pushed to new machines; developers can disable it.
func TestDeprecatedKeepsInstallState(t *testing.T) {
	reg := load(t, "org: acme\nskills:\n  p:old: deprecated\n  p:other: deprecated\n  p:gone: deprecated\n", "p:old", "p:other", "p:gone")
	plan := policy.Resolve(reg, policy.Prefs{
		Present:   []string{"p:old", "p:gone"},
		Installed: []string{"p:other"},
		Disabled:  []string{"p:gone"},
	})
	for id, want := range map[string]bool{"p:old": true, "p:other": true, "p:gone": false} {
		if e := entry(t, plan, id); e.Install != want || e.Status != registry.Deprecated || e.Deprecation == nil {
			t.Errorf("%s: %+v", id, e)
		}
	}
}

func TestPlanWanted(t *testing.T) {
	reg := load(t, "org: acme\nskills:\n  p:a: required\n  p:b: available\n", "p:a", "p:b")
	wanted := policy.Resolve(reg, policy.Prefs{}).Wanted()
	if len(wanted) != 1 || wanted[0].Skill.ID != "p:a" {
		t.Fatalf("wanted: %+v", wanted)
	}
}
