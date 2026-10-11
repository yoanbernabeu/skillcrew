// Package snapshot reads registries straight from git objects and caches
// exported skills by tree id: a tree never changes, so each skill version is
// exported once, and unchanged skills cost nothing.
package snapshot

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"time"

	"github.com/yoanbernabeu/skillcrew/internal/fsx"
	"github.com/yoanbernabeu/skillcrew/internal/gitx"
	"github.com/yoanbernabeu/skillcrew/internal/registry"
)

// Store caches exported skills under Dir/<tree id>.
type Store struct {
	Repo *gitx.Repo
	Dir  string
}

// Snapshot is a registry as of one commit.
type Snapshot struct {
	Commit   string
	Registry *registry.Registry
	tree     map[string]gitx.Entry
}

// TreeID returns the tree id of a directory of the snapshot.
func (s *Snapshot) TreeID(dir string) (string, bool) {
	e, ok := s.tree[dir]
	return e.OID, ok && e.Dir
}

// Registry loads the registry of commit without exporting it: only the
// metadata files are read from git.
func (s *Store) Registry(ctx context.Context, commit string) (*Snapshot, error) {
	tree, err := s.Repo.Tree(ctx, commit)
	if err != nil {
		return nil, err
	}
	fsys := newTreeFS(tree)
	for _, name := range []string{registry.MarketplaceFile, registry.GovernanceFile} {
		if e, ok := tree[name]; ok && !e.Dir {
			if fsys.data[name], err = s.Repo.Blob(ctx, e.OID); err != nil {
				return nil, err
			}
		}
	}
	reg, err := registry.Load(fsys)
	if err != nil {
		return nil, err
	}
	return &Snapshot{Commit: commit, Registry: reg, tree: tree}, nil
}

// Want is a tree to have in the cache: the directory Dir of Commit, whose
// tree id is Tree. Without a commit, the tree is listed by id.
type Want struct {
	Commit, Dir, Tree string
}

// Skill returns the directory holding the files of tree, exporting it if needed.
func (s *Store) Skill(ctx context.Context, tree string) (string, error) {
	dirs, errs := s.Export(ctx, []Want{{Tree: tree}})
	return dirs[0], errs[0]
}

// Export makes sure the wanted trees are in the cache and returns the
// directory holding each, or why it cannot. Missing trees are exported
// together (see gitx.ExportTrees). A tree rejected by the export limits is
// remembered and not tried again: trees never change.
func (s *Store) Export(ctx context.Context, wants []Want) ([]string, []error) {
	dirs, errs := make([]string, len(wants)), make([]error, len(wants))
	rejected := s.rejections()
	var trees []gitx.ExportTree
	var exported [][]int // the wants of each exported tree
	pending := map[string]int{}
	for i, w := range wants {
		if !gitx.IsObjectID(w.Tree) {
			errs[i] = fmt.Errorf("%q is not a tree id", w.Tree)
			continue
		}
		dirs[i] = filepath.Join(s.Dir, w.Tree)
		if _, err := os.Stat(dirs[i]); err == nil {
			continue
		}
		if reason, ok := rejected.find(w.Tree); ok {
			errs[i] = &gitx.RejectedError{Reason: reason}
			continue
		}
		if k, ok := pending[w.Tree]; ok {
			exported[k] = append(exported[k], i)
			continue
		}
		if err := os.MkdirAll(s.Dir, 0o700); err != nil {
			errs[i] = err
			continue
		}
		tmp, err := os.MkdirTemp(s.Dir, ".export-")
		if err != nil {
			errs[i] = err
			continue
		}
		defer os.RemoveAll(tmp)
		pending[w.Tree] = len(trees)
		trees = append(trees, gitx.ExportTree{Commit: w.Commit, Dir: w.Dir, OID: w.Tree, Dest: tmp})
		exported = append(exported, []int{i})
	}
	if len(trees) == 0 {
		return dirs, errs
	}
	changed := false
	for k, err := range s.Repo.ExportTrees(ctx, trees) {
		dir := filepath.Join(s.Dir, trees[k].OID)
		// Another process may have exported the same tree meanwhile: theirs is as good.
		if err == nil {
			if renameErr := os.Rename(trees[k].Dest, dir); renameErr != nil {
				if _, statErr := os.Stat(dir); statErr != nil {
					err = renameErr
				}
			}
		}
		var r *gitx.RejectedError
		if errors.As(err, &r) {
			rejected.add(trees[k].OID, r.Reason)
			changed = true
		}
		for _, i := range exported[k] {
			errs[i] = err
		}
	}
	if changed {
		// Only an optimization: failing to remember means trying again.
		_ = rejected.save(filepath.Join(s.Dir, rejectedFile))
	}
	return dirs, errs
}

// rejectedFile remembers the trees the export limits rejected.
const rejectedFile = ".rejected.json"

// maxRejected bounds the trees remembered.
const maxRejected = 100

type rejections struct {
	// Limits the rejections were made with: other limits may accept the trees.
	Limits string      `json:"limits"`
	Trees  []rejection `json:"trees"`
}

type rejection struct {
	Tree   string `json:"tree"`
	Reason string `json:"reason"`
}

func limits() string {
	return fmt.Sprintf("%d bytes, %d files", gitx.ExportMaxBytes, gitx.ExportMaxFiles)
}

func (s *Store) rejections() *rejections {
	r := &rejections{Limits: limits()}
	data, err := os.ReadFile(filepath.Join(s.Dir, rejectedFile))
	if err != nil {
		return r
	}
	var saved rejections
	if json.Unmarshal(data, &saved) == nil && saved.Limits == r.Limits {
		r.Trees = saved.Trees
	}
	return r
}

func (r *rejections) find(tree string) (string, bool) {
	for _, t := range r.Trees {
		if t.Tree == tree {
			return t.Reason, true
		}
	}
	return "", false
}

func (r *rejections) add(tree, reason string) {
	r.Trees = append(r.Trees, rejection{Tree: tree, Reason: reason})
	if len(r.Trees) > maxRejected {
		r.Trees = r.Trees[len(r.Trees)-maxRejected:]
	}
}

func (r *rejections) save(path string) error {
	data, err := json.Marshal(r)
	if err != nil {
		return err
	}
	return fsx.WriteFileAtomic(path, data, 0o600)
}

// Prune removes every cached skill except keep. Each one is renamed aside
// before being deleted, so an interrupted prune never leaves a partial tree
// that would pass for a complete one. Once ctx is done, the rest is left to
// the next sync.
func (s *Store) Prune(ctx context.Context, keep ...string) error {
	entries, err := os.ReadDir(s.Dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	kept := map[string]bool{rejectedFile: true}
	for _, k := range keep {
		kept[k] = true
	}
	var errs []error
	for _, e := range entries {
		if ctx.Err() != nil {
			break
		}
		if kept[e.Name()] {
			continue
		}
		trash, err := os.MkdirTemp(s.Dir, ".trash-")
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if err := os.Rename(filepath.Join(s.Dir, e.Name()), filepath.Join(trash, e.Name())); err != nil {
			errs = append(errs, err)
		}
		errs = append(errs, os.RemoveAll(trash))
	}
	return errors.Join(errs...)
}

// treeFS is a read-only view of a git tree: every directory and file exists,
// and only the files in data have content. It is all registry.Load needs.
type treeFS struct {
	tree map[string]gitx.Entry
	data map[string][]byte
	// children lists the entries of each directory, sorted, so listing a
	// directory never scans the whole tree.
	children map[string][]string
}

// newTreeFS indexes tree once: building the view is linear in its size.
func newTreeFS(tree map[string]gitx.Entry) treeFS {
	children := map[string][]string{}
	for p := range tree {
		dir := path.Dir(p)
		children[dir] = append(children[dir], p)
	}
	for _, names := range children {
		slices.Sort(names)
	}
	return treeFS{tree: tree, data: map[string][]byte{}, children: children}
}

func (t treeFS) entry(name string) (gitx.Entry, error) {
	if name == "." {
		return gitx.Entry{Dir: true}, nil
	}
	if e, ok := t.tree[name]; ok && fs.ValidPath(name) {
		return e, nil
	}
	return gitx.Entry{}, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
}

func (t treeFS) Open(name string) (fs.File, error) {
	e, err := t.entry(name)
	if err != nil {
		return nil, err
	}
	return &treeFile{info: t.info(name, e), Reader: bytes.NewReader(t.data[name])}, nil
}

func (t treeFS) Stat(name string) (fs.FileInfo, error) {
	e, err := t.entry(name)
	if err != nil {
		return nil, err
	}
	return t.info(name, e), nil
}

func (t treeFS) ReadFile(name string) ([]byte, error) {
	e, err := t.entry(name)
	if err == nil && e.Dir {
		err = &fs.PathError{Op: "read", Path: name, Err: fs.ErrInvalid}
	}
	return t.data[name], err
}

func (t treeFS) ReadDir(name string) ([]fs.DirEntry, error) {
	if e, err := t.entry(name); err != nil || !e.Dir {
		return nil, &fs.PathError{Op: "readdir", Path: name, Err: fs.ErrNotExist}
	}
	names := t.children[name]
	out := make([]fs.DirEntry, len(names))
	for i, p := range names {
		out[i] = fs.FileInfoToDirEntry(t.info(p, t.tree[p]))
	}
	return out, nil
}

func (t treeFS) info(name string, e gitx.Entry) fs.FileInfo {
	mode := fs.FileMode(0o644)
	if e.Dir {
		mode = fs.ModeDir | 0o755
	}
	return treeInfo{name: path.Base(name), size: int64(len(t.data[name])), mode: mode}
}

type treeInfo struct {
	name string
	size int64
	mode fs.FileMode
}

func (i treeInfo) Name() string       { return i.name }
func (i treeInfo) Size() int64        { return i.size }
func (i treeInfo) Mode() fs.FileMode  { return i.mode }
func (i treeInfo) ModTime() time.Time { return time.Time{} }
func (i treeInfo) IsDir() bool        { return i.mode.IsDir() }
func (i treeInfo) Sys() any           { return nil }

type treeFile struct {
	info fs.FileInfo
	*bytes.Reader
}

func (f *treeFile) Stat() (fs.FileInfo, error) { return f.info, nil }
func (f *treeFile) Close() error               { return nil }
