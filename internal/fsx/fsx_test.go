package fsx_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/yoanbernabeu/skillcrew/internal/fsx"
)

func TestWriteFileAtomic(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a", "b.txt")
	if err := fsx.WriteFileAtomic(path, []byte("one"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := fsx.WriteFileAtomic(path, []byte("two"), 0o600); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	info, _ := os.Stat(path)
	if string(b) != "two" || info.Mode().Perm() != 0o600 {
		t.Fatalf("got %q %v", b, info.Mode())
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 1 {
		t.Fatalf("temporary files left behind: %v", entries)
	}
}

func TestCopyDir(t *testing.T) {
	src := t.TempDir()
	if err := os.MkdirAll(filepath.Join(src, "d"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "d", "x.sh"), []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(t.TempDir(), "copy")
	if err := fsx.CopyDir(src, dst); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(dst, "d", "x.sh"))
	if err != nil || info.Mode().Perm() != 0o755 {
		t.Fatalf("copy failed: %v %v", info, err)
	}
}

func TestTryLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.lock")
	unlock, ok, err := fsx.TryLock(path)
	if err != nil || !ok {
		t.Fatalf("first lock: %v %v", ok, err)
	}
	if _, ok, _ := fsx.TryLock(path); ok {
		t.Fatal("second lock must fail while the first is held")
	}
	unlock()
	unlock2, ok, err := fsx.TryLock(path)
	if err != nil || !ok {
		t.Fatalf("lock after release: %v %v", ok, err)
	}
	unlock2()
}

func TestWriteFileAtomicFollowsSymlinks(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "dotfiles", "settings.json")
	if err := fsx.WriteFileAtomic(target, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "settings.json")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := fsx.WriteFileAtomic(link, []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	if info, _ := os.Lstat(link); info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("the symlink was replaced by a regular file")
	}
	if b, _ := os.ReadFile(target); string(b) != "new" {
		t.Fatalf("target content = %q", b)
	}
}

func TestLockWaits(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.lock")
	unlock, _, _ := fsx.TryLock(path)
	go func() {
		time.Sleep(100 * time.Millisecond)
		unlock()
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	release, err := fsx.Lock(ctx, path)
	if err != nil {
		t.Fatalf("the lock must be acquired once released: %v", err)
	}
	release()

	held, _, _ := fsx.TryLock(path)
	defer held()
	ctx, cancel = context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := fsx.Lock(ctx, path); err == nil {
		t.Fatal("Lock must give up when the context ends")
	}
}
