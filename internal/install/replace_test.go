package install

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/yoanbernabeu/skillcrew/internal/paths"
)

// When the staged copy cannot be moved into place, the previous content is
// put back, whatever happens to it otherwise.
func TestReplaceRollsBack(t *testing.T) {
	for name, kind := range map[string]backupKind{"discard": noBackup, "edit": editBackup, "personal": personalBackup} {
		t.Run(name, func(t *testing.T) {
			in := New(paths.Paths{Home: t.TempDir()})
			target := in.skillDir("review")
			if err := os.MkdirAll(target, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(target, "SKILL.md"), []byte("previous"), 0o644); err != nil {
				t.Fatal(err)
			}
			missing := filepath.Join(t.TempDir(), "review") // its parent exists
			if err := in.replace(missing, target, kind); err == nil {
				t.Fatal("replace must fail when the staged copy cannot be moved")
			}
			if b, err := os.ReadFile(filepath.Join(target, "SKILL.md")); err != nil || string(b) != "previous" {
				t.Fatalf("previous content not restored: %q %v", b, err)
			}
		})
	}
}
