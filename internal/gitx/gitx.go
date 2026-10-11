// Package gitx wraps the system git binary, so Skillcrew reuses the
// developer's existing credentials (credential helpers, SSH agent, gh).
package gitx

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"syscall"
	"time"
)

// Repo is a bare clone of a remote repository. It only holds what Skillcrew
// asked for: the followed branch under refs/remotes/origin and the pinned
// tags. Wildcard refspecs are never used: on case-insensitive filesystems a
// pushed "Main" branch or "V1" tag would overwrite "main" or "v1".
type Repo struct {
	Dir string
}

// Open returns the repository stored in dir.
func Open(dir string) *Repo { return &Repo{Dir: dir} }

// Clone makes a bare clone of url into dir holding the remote's default branch.
func Clone(ctx context.Context, url, dir string) (*Repo, error) {
	if _, err := run(ctx, "", "init", "--quiet", "--bare", dir); err != nil {
		return nil, err
	}
	r := Open(dir)
	if _, err := r.Git(ctx, "config", "remote.origin.url", url); err != nil {
		return nil, err
	}
	// One round trip finds the default branch and its commit; the branch
	// is then matched by exact name, as in lsRemote.
	out, err := r.Git(ctx, "ls-remote", "--symref", "origin", "--", "HEAD", "refs/heads/*")
	if err != nil {
		return nil, err
	}
	var branch string
	for _, line := range strings.Split(out, "\n") {
		if target, ok := strings.CutSuffix(line, "\tHEAD"); ok && strings.HasPrefix(target, "ref: refs/heads/") {
			branch = strings.TrimPrefix(target, "ref: refs/heads/")
		}
	}
	if branch == "" {
		return nil, fmt.Errorf("cannot find the default branch of %s", Redact(url))
	}
	ref := "refs/heads/" + branch
	if err := r.checkRef(ctx, ref); err != nil {
		return nil, err
	}
	oid, ok := remoteRefs(out, ref)[ref]
	if !ok {
		return nil, fmt.Errorf("not found on the remote: %s", ref)
	}
	if err := r.fetchObjects(ctx, oid); err != nil {
		return nil, err
	}
	if _, err := r.Git(ctx, "update-ref", "refs/remotes/origin/"+branch, oid); err != nil {
		return nil, err
	}
	_, err = r.Git(ctx, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/"+branch)
	return r, err
}

// Git runs a git command in the repository and returns its trimmed output.
func (r *Repo) Git(ctx context.Context, args ...string) (string, error) {
	return run(ctx, r.Dir, args...)
}

// Fetch updates one remote branch.
func (r *Repo) Fetch(ctx context.Context, branch string) error {
	ref := "refs/heads/" + branch
	oids, err := r.lsRemote(ctx, ref)
	if err != nil {
		return err
	}
	if err := r.fetchObjects(ctx, oids[ref]); err != nil {
		return err
	}
	_, err = r.Git(ctx, "update-ref", "refs/remotes/origin/"+branch, oids[ref])
	return err
}

// FetchTags fetches the given tags that are missing locally. Tags are
// immutable here: a tag known locally keeps its first commit and is never
// fetched again, so once every tag is known no network round trip is made.
// With verify, known tags are also compared with the remote, and those moved
// upstream are reported as an error (they still keep their first commit).
func (r *Repo) FetchTags(ctx context.Context, verify bool, tags ...string) error {
	tags = slices.Compact(slices.Sorted(slices.Values(tags)))
	local, err := r.localTags(ctx, tags...)
	if err != nil {
		return err
	}
	var query, missing []string
	for _, t := range tags {
		_, known := local[t]
		if !known {
			missing = append(missing, "refs/tags/"+t)
		}
		if !known || verify {
			query = append(query, "refs/tags/"+t)
		}
	}
	if len(query) == 0 {
		return nil
	}
	oids, err := r.lsRemote(ctx, query...)
	if err != nil {
		return err
	}
	if len(missing) > 0 {
		var want []string
		var create strings.Builder
		for _, ref := range missing {
			want = append(want, oids[ref])
			// create: never overwrite a tag that appeared meanwhile.
			create.WriteString("create " + ref + " " + oids[ref] + "\n")
		}
		if err := r.fetchObjects(ctx, want...); err != nil {
			return err
		}
		if _, err := r.input(ctx, create.String(), "update-ref", "--stdin"); err != nil {
			return err
		}
	}
	var moved []string
	for _, t := range tags {
		if oid, known := local[t]; known && verify && oid != oids["refs/tags/"+t] {
			moved = append(moved, t)
		}
	}
	if len(moved) > 0 {
		return fmt.Errorf("tags moved upstream, keeping their first commit: %s", strings.Join(moved, ", "))
	}
	return nil
}

// localTags returns the object id of the given tags that exist locally,
// matched by exact name: for-each-ref also matches refs below a pattern, so
// its output is filtered.
func (r *Repo) localTags(ctx context.Context, tags ...string) (map[string]string, error) {
	found := map[string]string{}
	if len(tags) == 0 {
		return found, nil
	}
	args := []string{"for-each-ref", "--format=%(objectname) %(refname)"}
	for _, t := range tags {
		args = append(args, "refs/tags/"+t)
	}
	out, err := r.Git(ctx, args...)
	if err != nil {
		return nil, err
	}
	for _, line := range strings.Split(out, "\n") {
		oid, ref, _ := strings.Cut(line, " ")
		if name, ok := strings.CutPrefix(ref, "refs/tags/"); ok && IsObjectID(oid) && slices.Contains(tags, name) {
			found[name] = oid
		}
	}
	return found, nil
}

// lsRemote returns the object ids of remote refs matching these exact names.
// Names are matched by equality, never by git's "did you mean" rules, so no
// other remote ref is ever considered; a missing ref is an error.
func (r *Repo) lsRemote(ctx context.Context, refs ...string) (map[string]string, error) {
	for _, ref := range refs {
		if err := r.checkRef(ctx, ref); err != nil {
			return nil, err
		}
	}
	out, err := r.Git(ctx, append([]string{"ls-remote", "origin", "--"}, refs...)...)
	if err != nil {
		return nil, err
	}
	oids := remoteRefs(out, refs...)
	var missing []string
	for _, ref := range refs {
		if _, ok := oids[ref]; !ok {
			missing = append(missing, ref)
		}
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("not found on the remote: %s", strings.Join(missing, ", "))
	}
	return oids, nil
}

// remoteRefs parses ls-remote output, keeping the refs named exactly refs.
func remoteRefs(out string, refs ...string) map[string]string {
	oids := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		if oid, name, ok := strings.Cut(line, "\t"); ok && slices.Contains(refs, name) && IsObjectID(oid) {
			oids[name] = oid
		}
	}
	return oids
}

// fetchObjects downloads objects by id, so no remote ref name is involved.
func (r *Repo) fetchObjects(ctx context.Context, oids ...string) error {
	_, err := r.Git(ctx, append([]string{"fetch", "--quiet", "--no-tags", "--no-write-fetch-head", "origin"}, oids...)...)
	return err
}

func (r *Repo) checkRef(ctx context.Context, ref string) error {
	if _, err := r.Git(ctx, "check-ref-format", ref); err != nil {
		return fmt.Errorf("invalid ref name %q", ref)
	}
	return nil
}

// RemoteURL returns the URL of origin.
func (r *Repo) RemoteURL(ctx context.Context) (string, error) {
	return r.Git(ctx, "config", "--get", "remote.origin.url")
}

// DefaultBranch returns the remote's default branch.
func (r *Repo) DefaultBranch(ctx context.Context) (string, error) {
	ref, err := r.Git(ctx, "symbolic-ref", "--short", "refs/remotes/origin/HEAD")
	return strings.TrimPrefix(ref, "origin/"), err
}

// Branch returns the commit a remote branch points to.
func (r *Repo) Branch(ctx context.Context, name string) (string, error) {
	return r.exact(ctx, "refs/remotes/origin/"+name, "branch "+name)
}

// Tag returns the commit a tag points to.
func (r *Repo) Tag(ctx context.Context, name string) (string, error) {
	return r.exact(ctx, "refs/tags/"+name, "tag "+name)
}

var commitID = regexp.MustCompile(`^([0-9a-f]{40}|[0-9a-f]{64})$`)

// IsObjectID reports whether s is a full object id (commit, tree…).
func IsObjectID(s string) bool { return commitID.MatchString(s) }

// Commit checks that a full commit id exists. Abbreviated ids and ref names
// are rejected, so a branch can never stand in for a pinned commit.
func (r *Repo) Commit(ctx context.Context, sha string) (string, error) {
	if !IsObjectID(sha) {
		return "", fmt.Errorf("%q is not a full commit id", sha)
	}
	return r.peel(ctx, sha, "commit "+sha)
}

// exact resolves a full ref name without git's "did you mean" lookup, so a
// tag can never stand in for a missing branch or the other way round.
func (r *Repo) exact(ctx context.Context, ref, what string) (string, error) {
	if err := r.checkRef(ctx, ref); err != nil {
		return "", fmt.Errorf("invalid %s", what)
	}
	oid, err := r.Git(ctx, "show-ref", "--verify", "--hash", ref)
	if err != nil {
		return "", fmt.Errorf("unknown %s", what)
	}
	return r.peel(ctx, oid, what)
}

func (r *Repo) peel(ctx context.Context, oid, what string) (string, error) {
	sha, err := r.Git(ctx, "rev-parse", "--verify", "--quiet", "--end-of-options", oid+"^{commit}")
	if err != nil {
		return "", fmt.Errorf("unknown %s", what)
	}
	return sha, nil
}

// Pin is a directory of the registry at a pinned tag or commit.
type Pin struct {
	Tag string // tag name, without refs/tags/
	SHA string // full commit id, when Tag is empty
	Dir string // slash-separated, relative to the root
}

// Target is what a pin resolves to.
type Target struct {
	Commit, Tree string
	Err          error
}

// ResolvePins resolves pins to their commit and the tree of their directory
// with at most two git commands, whatever their number. Tags are matched by
// exact name, never by git's "did you mean" rules, and commits must be full
// ids, as with Tag and Commit.
func (r *Repo) ResolvePins(ctx context.Context, pins []Pin) []Target {
	out := make([]Target, len(pins))
	var tags []string
	for _, p := range pins {
		if p.Tag != "" {
			tags = append(tags, p.Tag)
		}
	}
	local, err := r.localTags(ctx, tags...)
	if err != nil {
		for i := range out {
			out[i].Err = err
		}
		return out
	}
	var queries []string
	var asked []int // index of the pin of each pair of queries
	for i, p := range pins {
		oid := p.SHA
		switch {
		case p.Tag != "":
			if oid = local[p.Tag]; oid == "" {
				out[i].Err = fmt.Errorf("unknown tag %s", p.Tag)
				continue
			}
		case !IsObjectID(oid):
			out[i].Err = fmt.Errorf("%q is not a full commit id", oid)
			continue
		}
		// One query per line: a directory must not smuggle another one in.
		if strings.ContainsFunc(p.Dir, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
			out[i].Err = fmt.Errorf("invalid directory %q", p.Dir)
			continue
		}
		tree := oid + "^{commit}:" + p.Dir
		if p.Dir == "." || p.Dir == "" {
			tree = oid + "^{commit}^{tree}"
		}
		queries = append(queries, oid+"^{commit}", tree)
		asked = append(asked, i)
	}
	if len(queries) == 0 {
		return out
	}
	objects, err := r.batchCheck(ctx, queries)
	for k, i := range asked {
		p := pins[i]
		what := "commit " + p.SHA
		if p.Tag != "" {
			what = "tag " + p.Tag
		}
		switch commit, tree := objects[2*k], objects[2*k+1]; {
		case err != nil:
			out[i].Err = err
		case commit.kind != "commit":
			out[i].Err = fmt.Errorf("unknown %s", what)
		case tree.kind != "tree":
			out[i].Err = fmt.Errorf("%s not found at commit %.12s", p.Dir, commit.oid)
		default:
			out[i] = Target{Commit: commit.oid, Tree: tree.oid}
		}
	}
	return out
}

type object struct{ oid, kind string }

// batchCheck looks up objects by name with a single git command; a missing
// object has an empty kind.
func (r *Repo) batchCheck(ctx context.Context, names []string) ([]object, error) {
	out, err := r.input(ctx, strings.Join(names, "\n")+"\n", "cat-file", "--batch-check")
	if err != nil {
		return make([]object, len(names)), err
	}
	lines := strings.Split(out, "\n")
	if len(lines) != len(names) {
		return make([]object, len(names)), fmt.Errorf("git cat-file: %d answers to %d queries", len(lines), len(names))
	}
	objects := make([]object, len(names))
	for i, line := range lines {
		// "<oid> <type> <size>", or "<name> missing" (the name may hold spaces).
		if f := strings.Fields(line); len(f) == 3 && IsObjectID(f[0]) && isNumber(f[2]) {
			objects[i] = object{oid: f[0], kind: f[1]}
		}
	}
	return objects, nil
}

func isNumber(s string) bool {
	return s != "" && strings.Trim(s, "0123456789") == ""
}

// Entry is an object of a commit's tree.
type Entry struct {
	OID string
	Dir bool
}

// Tree lists every file and directory of a commit, keyed by slash-separated path.
func (r *Repo) Tree(ctx context.Context, commit string) (map[string]Entry, error) {
	out, err := r.output(ctx, "ls-tree", "-r", "-t", "-z", "--full-tree", "--end-of-options", commit)
	if err != nil {
		return nil, err
	}
	entries := map[string]Entry{}
	for _, line := range bytes.Split(out, []byte{0}) {
		meta, path, ok := bytes.Cut(line, []byte{'\t'})
		if fields := strings.Fields(string(meta)); ok && len(fields) == 3 {
			entries[string(path)] = Entry{OID: fields[2], Dir: fields[1] == "tree"}
		}
	}
	return entries, nil
}

// Blob returns the content of a blob.
func (r *Repo) Blob(ctx context.Context, oid string) ([]byte, error) {
	return r.output(ctx, "cat-file", "blob", oid)
}

// Limits on what Export writes, so a hostile registry cannot fill the disk.
var (
	ExportMaxBytes int64 = 256 << 20
	ExportMaxFiles       = 20000
)

// RejectedError is an export failure that would happen again with the same
// tree: too many files or bytes, or paths this filesystem cannot hold apart.
type RejectedError struct{ Reason string }

func (e *RejectedError) Error() string { return e.Reason }

func rejected(format string, args ...any) error {
	return &RejectedError{Reason: fmt.Sprintf(format, args...)}
}

// ExportTree is a tree to export into Dest: the directory Dir of Commit,
// whose tree id must be OID. Without a commit, the tree OID is listed directly.
type ExportTree struct {
	Commit, Dir, OID string
	Dest             string
}

// Export writes the files of a commit or tree id into dest, straight from
// git objects: no .gitattributes rule, filter driver, ident or eol
// conversion applies, so what is written is exactly what was reviewed.
// Symbolic links and submodules are skipped. Two paths that name the same
// file on this filesystem (case or Unicode normalization) fail the export.
func (r *Repo) Export(ctx context.Context, treeish, dest string) error {
	return r.ExportTrees(ctx, []ExportTree{{OID: treeish, Dest: dest}})[0]
}

// ExportTrees exports trees like Export, with one listing per commit and a
// single object stream for all of them. Trees over the limits are rejected
// from their listing, before any content is read. It returns the error of
// each tree.
func (r *Repo) ExportTrees(ctx context.Context, trees []ExportTree) []error {
	jobs := make([]exportJob, len(trees))
	byCommit := map[string][]int{}
	var commits []string
	for i, t := range trees {
		jobs[i].dest = t.Dest
		if t.Commit == "" || t.Dir == "" || t.Dir == "." {
			jobs[i].list(r.output(ctx, "ls-tree", "-r", "-l", "-z", "--full-tree", "--end-of-options", t.OID))
			continue
		}
		if _, ok := byCommit[t.Commit]; !ok {
			commits = append(commits, t.Commit)
		}
		byCommit[t.Commit] = append(byCommit[t.Commit], i)
	}
	for _, commit := range commits {
		r.listDirs(ctx, commit, trees, jobs, byCommit[commit])
	}
	for i := range jobs {
		jobs[i].checkLimits()
	}
	r.stream(ctx, jobs)
	errs := make([]error, len(jobs))
	for i, j := range jobs {
		errs[i] = j.err
	}
	return errs
}

type blob struct {
	path, oid, mode string
	size            int64
}

type exportJob struct {
	dest  string
	blobs []blob
	err   error
}

// entries parses "ls-tree -l -z" output.
func entries(out []byte, fn func(mode, kind, oid string, size int64, path string)) {
	for _, line := range bytes.Split(out, []byte{0}) {
		meta, path, ok := bytes.Cut(line, []byte{'\t'})
		fields := strings.Fields(string(meta))
		if !ok || len(fields) != 4 {
			continue
		}
		var size int64
		if fields[1] == "blob" {
			if _, err := fmt.Sscan(fields[3], &size); err != nil {
				size = -1
			}
		}
		fn(fields[0], fields[1], fields[2], size, string(path))
	}
}

func (j *exportJob) add(mode, kind, oid string, size int64, path string) {
	// Symbolic links (120000) and submodules (commit) are not exported.
	if kind == "blob" && mode != "120000" {
		j.blobs = append(j.blobs, blob{path: path, oid: oid, mode: mode, size: size})
	}
}

// list fills the job from the listing of a tree.
func (j *exportJob) list(out []byte, err error) {
	if err != nil {
		j.err = err
		return
	}
	entries(out, func(mode, kind, oid string, size int64, path string) { j.add(mode, kind, oid, size, path) })
}

// listDirs lists several directories of a commit with a single ls-tree.
// Each file goes to every wanted directory holding it, and each directory
// must have the expected tree id.
func (r *Repo) listDirs(ctx context.Context, commit string, trees []ExportTree, jobs []exportJob, idx []int) {
	args := []string{"ls-tree", "-r", "-t", "-l", "-z", "--full-tree", "--end-of-options", commit, "--"}
	byDir := map[string][]int{}
	for _, i := range idx {
		if _, ok := byDir[trees[i].Dir]; !ok {
			args = append(args, trees[i].Dir)
		}
		byDir[trees[i].Dir] = append(byDir[trees[i].Dir], i)
	}
	out, err := r.output(ctx, args...)
	if err != nil {
		for _, i := range idx {
			jobs[i].err = err
		}
		return
	}
	found := map[int]bool{}
	entries(out, func(mode, kind, oid string, size int64, p string) {
		if kind == "tree" {
			for _, i := range byDir[p] {
				found[i] = true
				if oid != trees[i].OID && jobs[i].err == nil {
					jobs[i].err = fmt.Errorf("%s is tree %s at commit %.12s, not %s", p, oid, commit, trees[i].OID)
				}
			}
			return
		}
		for dir := path.Dir(p); dir != "."; dir = path.Dir(dir) {
			for _, i := range byDir[dir] {
				jobs[i].add(mode, kind, oid, size, strings.TrimPrefix(p, dir+"/"))
			}
		}
	})
	for _, i := range idx {
		if !found[i] && jobs[i].err == nil {
			jobs[i].err = fmt.Errorf("%s not found at commit %.12s", trees[i].Dir, commit)
		}
	}
}

// checkLimits rejects a tree from its listing, before any content is read.
func (j *exportJob) checkLimits() {
	if j.err != nil {
		return
	}
	if len(j.blobs) > ExportMaxFiles {
		j.err = rejected("tree exceeds %d files", ExportMaxFiles)
		return
	}
	var total int64
	for _, b := range j.blobs {
		if b.size < 0 {
			j.err = fmt.Errorf("unknown size for %q", b.path)
			return
		}
		total += b.size
	}
	if total > ExportMaxBytes {
		j.err = rejected("tree exceeds %d bytes", ExportMaxBytes)
	}
}

// stream reads the blobs of every job with a single cat-file process and
// writes them. A failing job does not stop the others.
func (r *Repo) stream(ctx context.Context, jobs []exportJob) {
	var input bytes.Buffer
	for _, j := range jobs {
		if j.err == nil {
			for _, b := range j.blobs {
				input.WriteString(b.oid + "\n")
			}
		}
	}
	if input.Len() == 0 {
		return
	}
	cmd := command(ctx, r.Dir, "cat-file", "--batch")
	cmd.Stdin = &input
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	fail := func(err error) {
		for i := range jobs {
			if jobs[i].err == nil {
				jobs[i].err = err
			}
		}
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		fail(err)
		return
	}
	if err := cmd.Start(); err != nil {
		fail(err)
		return
	}
	readErr := func() error {
		br := bufio.NewReader(stdout)
		for i := range jobs {
			j := &jobs[i]
			if j.err != nil {
				continue
			}
			var total int64
			for _, b := range j.blobs {
				header, err := br.ReadString('\n')
				if err != nil {
					return err
				}
				var oid, kind string
				var size int64
				if _, err := fmt.Sscanf(header, "%s %s %d", &oid, &kind, &size); err != nil || kind != "blob" || oid != b.oid {
					return fmt.Errorf("unexpected object %q", strings.TrimSpace(header))
				}
				content := io.LimitReader(br, size)
				if total += size; total > ExportMaxBytes && j.err == nil {
					j.err = rejected("tree exceeds %d bytes", ExportMaxBytes)
				}
				if j.err == nil {
					mode := os.FileMode(0o644)
					if b.mode == "100755" {
						mode = 0o755
					}
					j.err = writeBlob(j.dest, b.path, content, mode)
				}
				// Skip what was not written, keeping the stream in step.
				if _, err := io.Copy(io.Discard, content); err != nil {
					return err
				}
				if _, err := br.Discard(1); err != nil { // trailing newline
					return err
				}
			}
		}
		return nil
	}()
	_, _ = io.Copy(io.Discard, stdout)
	if err := cmd.Wait(); err != nil {
		fail(commandError([]string{"cat-file", "--batch"}, stderr.String(), err))
		return
	}
	if readErr != nil {
		fail(readErr)
	}
}

// writeBlob creates dest/path, refusing to overwrite or follow anything.
func writeBlob(dest, path string, r io.Reader, mode os.FileMode) error {
	target := filepath.Join(dest, filepath.FromSlash(path))
	if !strings.HasPrefix(target, filepath.Clean(dest)+string(filepath.Separator)) {
		return rejected("path %q escapes the destination", path)
	}
	err := os.MkdirAll(filepath.Dir(target), 0o755)
	var f *os.File
	if err == nil {
		f, err = os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY|syscall.O_NOFOLLOW, mode)
	}
	if errors.Is(err, os.ErrExist) || errors.Is(err, syscall.ENOTDIR) {
		return rejected("cannot write %q: two paths name the same file on this filesystem", path)
	}
	if err != nil {
		return fmt.Errorf("cannot write %q: %w", path, err)
	}
	if _, err := io.Copy(f, r); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// NeutralizeAttributes overrides every in-tree .gitattributes rule that runs
// commands or rewrites content, for commands that check files out (propose).
func (r *Repo) NeutralizeAttributes() error {
	path := filepath.Join(r.Dir, "info", "attributes")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte("* -filter -ident -text -eol -working-tree-encoding -export-subst -export-ignore\n"), 0o644)
}

// hardening applies to every git command: never run repository hooks, never
// use the ext:: transport, never spawn a filesystem monitor.
var hardening = []string{"-c", "core.hooksPath=/dev/null", "-c", "protocol.ext.allow=never", "-c", "core.fsmonitor=false"}

func command(ctx context.Context, dir string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "git", append(slices.Clone(hardening), args...)...)
	cmd.Dir = dir
	// Never prompt: Skillcrew often runs from agent hooks and timers.
	cmd.Env = append(cleanEnv(), "GIT_TERMINAL_PROMPT=0", "LC_ALL=C",
		// Give up on stalled HTTP transfers instead of hanging an agent hook.
		"GIT_HTTP_LOW_SPEED_LIMIT=1000", "GIT_HTTP_LOW_SPEED_TIME=10")
	// A new session has no controlling terminal, so ssh cannot prompt either
	// (the user's own ssh command and keys still apply); askpass is disabled.
	// On cancellation, git and its helpers (remote-https, ssh) are killed together.
	cmd.Env = append(cmd.Env, "SSH_ASKPASS_REQUIRE=never", "GIT_LITERAL_PATHSPECS=1")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = time.Second
	return cmd
}

// localEnv are the variables that would point git at another repository or
// inject configuration (git rev-parse --local-env-vars, plus config overrides).
var localEnv = []string{
	"GIT_ALTERNATE_OBJECT_DIRECTORIES", "GIT_COMMON_DIR", "GIT_CONFIG", "GIT_CONFIG_COUNT",
	"GIT_CONFIG_PARAMETERS", "GIT_DIR", "GIT_GRAFT_FILE", "GIT_IMPLICIT_WORK_TREE",
	"GIT_INDEX_FILE", "GIT_INTERNAL_SUPER_PREFIX", "GIT_NAMESPACE", "GIT_NO_REPLACE_OBJECTS",
	"GIT_OBJECT_DIRECTORY", "GIT_PREFIX", "GIT_REPLACE_REF_BASE", "GIT_SHALLOW_FILE", "GIT_WORK_TREE",
}

func cleanEnv() []string {
	return slices.DeleteFunc(os.Environ(), func(kv string) bool {
		name, _, _ := strings.Cut(kv, "=")
		return slices.Contains(localEnv, name) || strings.HasPrefix(name, "GIT_CONFIG_KEY_") || strings.HasPrefix(name, "GIT_CONFIG_VALUE_")
	})
}

func run(ctx context.Context, dir string, args ...string) (string, error) {
	out, err := output(ctx, dir, args...)
	return strings.TrimSpace(string(out)), err
}

// input runs a git command fed with stdin and returns its trimmed output.
func (r *Repo) input(ctx context.Context, stdin string, args ...string) (string, error) {
	cmd := command(ctx, r.Dir, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdin, cmd.Stdout, cmd.Stderr = strings.NewReader(stdin), &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return "", commandError(args, stderr.String(), err)
	}
	return strings.TrimSpace(stdout.String()), nil
}

func (r *Repo) output(ctx context.Context, args ...string) ([]byte, error) {
	return output(ctx, r.Dir, args...)
}

func output(ctx context.Context, dir string, args ...string) ([]byte, error) {
	cmd := command(ctx, dir, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return nil, commandError(args, stderr.String(), err)
	}
	return stdout.Bytes(), nil
}

func commandError(args []string, stderr string, err error) error {
	msg := strings.TrimSpace(stderr)
	if msg == "" {
		msg = err.Error()
	}
	return fmt.Errorf("git %s: %s", Redact(strings.Join(args, " ")), Redact(msg))
}

var credentials = regexp.MustCompile(`(https?://)[^/@\s]+@`)

// Redact hides credentials embedded in URLs.
func Redact(s string) string { return credentials.ReplaceAllString(s, "${1}***@") }

var scpLike = regexp.MustCompile(`^([\w.-]+@)?[\w.-]+:`)

// NormalizeURL turns the "host/owner/repo" shorthand into an HTTPS clone URL.
// URLs, scp-like addresses and explicit local paths (absolute, ./ or ../)
// are returned unchanged: a shorthand never silently becomes a directory.
func NormalizeURL(s string) string {
	if strings.Contains(s, "://") || scpLike.MatchString(s) || filepath.IsAbs(s) ||
		strings.HasPrefix(s, "./") || strings.HasPrefix(s, "../") {
		return s
	}
	return "https://" + strings.TrimSuffix(s, ".git") + ".git"
}
