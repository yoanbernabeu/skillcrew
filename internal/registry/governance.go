package registry

import (
	"fmt"
	"slices"

	"gopkg.in/yaml.v3"
)

// GovernanceFile is the governance file at the registry root.
const GovernanceFile = "skillcrew.yaml"

// Status is the lifecycle status of a skill.
type Status string

// Skill statuses.
const (
	Available   Status = "available"
	Recommended Status = "recommended"
	Required    Status = "required"
	Deprecated  Status = "deprecated"
	Blocked     Status = "blocked"
)

// UnmarshalYAML rejects unknown statuses.
func (s *Status) UnmarshalYAML(node *yaml.Node) error {
	switch v := Status(node.Value); v {
	case Available, Recommended, Required, Deprecated, Blocked:
		*s = v
		return nil
	}
	return fmt.Errorf("line %d: unknown status %q", node.Line, node.Value)
}

// Rule is the governance of one skill at one level.
type Rule struct {
	Status     Status `yaml:"status"`
	Message    string `yaml:"message"`
	ReplacedBy string `yaml:"replaced_by"`
	Ref        string `yaml:"ref"`
	SHA        string `yaml:"sha"`
	InstallAs  string `yaml:"install_as"`
}

// UnmarshalYAML accepts either a bare status or a mapping.
func (r *Rule) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == yaml.ScalarNode {
		r.Status = ""
		return node.Decode(&r.Status)
	}
	if err := rejectUnknownKeys(node, "status", "message", "replaced_by", "ref", "sha", "install_as"); err != nil {
		return err
	}
	type plain Rule
	return node.Decode((*plain)(r))
}

// Pinned reports whether the rule fixes the skill to a ref or a commit.
func (r Rule) Pinned() bool { return r.Ref != "" || r.SHA != "" }

// Team holds the rules of one team.
type Team struct {
	Skills map[string]Rule `yaml:"skills"`
}

// UnmarshalYAML rejects unknown fields.
func (t *Team) UnmarshalYAML(node *yaml.Node) error {
	return eachField(node, []string{"skills"}, func(_ string, value *yaml.Node) error {
		return decodeMapping(value, &t.Skills)
	})
}

// Governance is the content of skillcrew.yaml.
type Governance struct {
	Org     string          `yaml:"org"`
	Default Status          `yaml:"default"`
	Skills  map[string]Rule `yaml:"skills"`
	Teams   map[string]Team `yaml:"teams"`
}

// OrgRule returns the organization rule for a skill, falling back to the default status.
func (g Governance) OrgRule(id string) Rule {
	r, ok := g.Skills[id]
	if !ok || r.Status == "" {
		r.Status = g.Default
	}
	return r
}

func parseGovernance(data []byte) (Governance, error) {
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return Governance{}, err
	}
	g := Governance{Default: Available}
	if len(root.Content) == 0 {
		return g, nil
	}
	err := eachField(root.Content[0], []string{"org", "default", "skills", "teams"}, func(key string, value *yaml.Node) error {
		switch key {
		case "org":
			return value.Decode(&g.Org)
		case "default":
			return value.Decode(&g.Default)
		case "skills":
			return decodeMapping(value, &g.Skills)
		default:
			return decodeMapping(value, &g.Teams)
		}
	})
	if err != nil {
		return Governance{}, err
	}
	if g.Default == "" {
		g.Default = Available
	}
	return g, nil
}

func rejectUnknownKeys(node *yaml.Node, allowed ...string) error {
	if node.Kind != yaml.MappingNode {
		return fmt.Errorf("line %d: expected a mapping", node.Line)
	}
	for i := 0; i < len(node.Content); i += 2 {
		key := node.Content[i]
		if !slices.Contains(allowed, key.Value) {
			return fmt.Errorf("line %d: unknown field %q", key.Line, key.Value)
		}
	}
	return nil
}

// eachField calls fn on the fields of a mapping, rejecting unknown and
// duplicate fields.
func eachField(node *yaml.Node, allowed []string, fn func(key string, value *yaml.Node) error) error {
	if err := rejectUnknownKeys(node, allowed...); err != nil {
		return err
	}
	if err := checkDuplicates(node); err != nil {
		return err
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		if err := fn(node.Content[i].Value, node.Content[i+1]); err != nil {
			return err
		}
	}
	return nil
}

// decodeMapping decodes a mapping into m. yaml.v3 compares every pair of keys
// of a mapping to find duplicates, which takes seconds on thousands of
// rules; a set does it in linear time. Anything but a plain mapping (null,
// merge keys, invalid values) is left to yaml.v3 and its own rules.
func decodeMapping[V any](node *yaml.Node, m *map[string]V) error {
	if node.Kind == yaml.AliasNode && node.Alias != nil {
		node = node.Alias
	}
	if node.Kind != yaml.MappingNode || hasMergeKey(node) {
		return node.Decode(m)
	}
	if err := checkDuplicates(node); err != nil {
		return err
	}
	out := make(map[string]V, len(node.Content)/2)
	for i := 0; i+1 < len(node.Content); i += 2 {
		var key string
		if err := node.Content[i].Decode(&key); err != nil {
			return err
		}
		var value V
		if err := node.Content[i+1].Decode(&value); err != nil {
			return err
		}
		out[key] = value
	}
	*m = out
	return nil
}

// checkDuplicates rejects a mapping that defines a key twice, as yaml.v3 does.
func checkDuplicates(node *yaml.Node) error {
	type key struct {
		kind  yaml.Kind
		value string
	}
	seen := make(map[key]int, len(node.Content)/2)
	for i := 0; i+1 < len(node.Content); i += 2 {
		k := node.Content[i]
		if line, dup := seen[key{k.Kind, k.Value}]; dup {
			return fmt.Errorf("line %d: mapping key %q already defined at line %d", k.Line, k.Value, line)
		}
		seen[key{k.Kind, k.Value}] = k.Line
	}
	return nil
}

func hasMergeKey(node *yaml.Node) bool {
	for i := 0; i < len(node.Content); i += 2 {
		if k := node.Content[i]; k.Kind == yaml.ScalarNode && k.ShortTag() == "!!merge" {
			return true
		}
	}
	return false
}
