package gitx

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Git rejects such tree entries, but Export must not rely on it.
func TestWriteBlobRefusesToEscape(t *testing.T) {
	root := t.TempDir()
	dest := filepath.Join(root, "dest")
	for _, path := range []string{"../escape", "a/../../escape", "..", "a/../../dest-sibling/x"} {
		if err := writeBlob(dest, path, strings.NewReader("pwned"), 0o644); err == nil {
			t.Errorf("writeBlob(%q) must fail", path)
		}
	}
	entries, _ := os.ReadDir(root)
	for _, e := range entries {
		if e.Name() != "dest" {
			t.Errorf("written outside the destination: %s", e.Name())
		}
	}
	if err := writeBlob(dest, "ok/file", strings.NewReader("fine"), 0o644); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(dest, "ok", "file")); string(b) != "fine" {
		t.Fatalf("content = %q", b)
	}
}
