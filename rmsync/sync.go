// Package rmsync mirrors a reMarkable tablet's documents into a local folder.
//
// The sync is one-way (tablet → local). Each document is written as one file
// per configured format, at the same folder path as on the tablet. Documents
// renamed or moved on the tablet are moved locally without re-downloading;
// documents deleted from the tablet are moved to Dest/.trash/.
package rmsync

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/kvitrvn/mimir-remarkable/remarkable"
)

// DefaultFormats are used when Syncer.Formats is empty.
var DefaultFormats = []remarkable.Format{remarkable.FormatPDF, remarkable.FormatRMDoc}

// Syncer mirrors the tablet into Dest.
type Syncer struct {
	Client *remarkable.Client
	// Dest is the local root folder; it is created if missing.
	Dest string
	// Formats to download for each document (DefaultFormats if empty).
	Formats []remarkable.Format
	// Concurrency is the number of parallel downloads (1 if < 1). The tablet
	// renders PDFs itself, so more than 2 rarely helps.
	Concurrency int
	// Logger receives progress; discarded if nil.
	Logger *slog.Logger
}

// Report summarises a sync run.
type Report struct {
	Added, Updated, Moved, Trashed int
	// Errors are per-document failures; the other documents were still synced.
	Errors []error
}

// Changed reports whether the run changed anything locally.
func (r Report) Changed() bool { return r.Added+r.Updated+r.Moved+r.Trashed > 0 }

func (r Report) String() string {
	var parts []string
	for _, p := range []struct {
		n    int
		what string
	}{{r.Added, "added"}, {r.Updated, "updated"}, {r.Moved, "moved"}, {r.Trashed, "trashed"}, {len(r.Errors), "errors"}} {
		if p.n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", p.n, p.what))
		}
	}
	if len(parts) == 0 {
		return "up to date"
	}
	return strings.Join(parts, ", ")
}

type run struct {
	s         *Syncer
	ctx       context.Context
	log       *slog.Logger
	dest      string
	formats   []remarkable.Format
	statePath string

	mu     sync.Mutex // guards st and report
	st     *state
	report Report
}

type job struct {
	entry   remarkable.Entry
	path    string
	formats []remarkable.Format
}

// Run performs one sync. It returns an error only if the sync could not run
// at all (tablet unreachable, unreadable state, cancelled context);
// per-document failures are in Report.Errors.
func (s *Syncer) Run(ctx context.Context) (Report, error) {
	if s.Client == nil || s.Dest == "" {
		return Report{}, errors.New("rmsync: Client and Dest are required")
	}
	dest, err := filepath.Abs(s.Dest)
	if err != nil {
		return Report{}, err
	}
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return Report{}, err
	}
	r := &run{
		s:         s,
		ctx:       ctx,
		log:       cmp.Or(s.Logger, slog.New(slog.DiscardHandler)),
		dest:      dest,
		formats:   s.Formats,
		statePath: filepath.Join(dest, stateFile),
	}
	if len(r.formats) == 0 {
		r.formats = DefaultFormats
	}
	r.removeStaleParts()
	if r.st, err = loadState(r.statePath); err != nil {
		return Report{}, err
	}

	nodes, err := s.Client.Walk(ctx)
	if err != nil {
		return Report{}, fmt.Errorf("list tablet: %w", err)
	}
	docPaths, dirPaths := layout(nodes, r.st.Docs)
	entries := map[string]remarkable.Entry{}
	for _, n := range nodes {
		if !n.IsFolder() {
			entries[n.ID] = n.Entry
		}
	}
	ids := slices.SortedFunc(maps.Keys(docPaths), func(a, b string) int { return strings.Compare(docPaths[a], docPaths[b]) })

	// 1. Deleted on the tablet → .trash. First, so their names are free.
	if len(docPaths) == 0 && len(r.st.Docs) > 0 {
		r.log.Warn("tablet lists no documents; not trashing anything", "tracked", len(r.st.Docs))
	} else {
		for _, id := range slices.Sorted(maps.Keys(r.st.Docs)) {
			if _, ok := docPaths[id]; !ok {
				r.trashDoc(id)
			}
		}
	}

	// 2. Mirror folders, including empty ones.
	for _, d := range dirPaths {
		if err := os.MkdirAll(r.abs(d), 0o755); err != nil {
			r.fail("folder "+d, err)
		}
	}

	// 3. Renamed or moved on the tablet → move locally.
	for _, id := range ids {
		if ds, ok := r.st.Docs[id]; ok && ds.Path != docPaths[id] {
			r.moveDoc(id, docPaths[id])
		}
	}

	// 4. New, modified or missing locally → download.
	var jobs []job
	for _, id := range ids {
		if f := r.needed(entries[id], docPaths[id]); len(f) > 0 {
			jobs = append(jobs, job{entries[id], docPaths[id], f})
		} else {
			r.log.Debug("unchanged", "path", docPaths[id])
		}
	}
	sem := make(chan struct{}, max(s.Concurrency, 1))
	var wg sync.WaitGroup
	for _, j := range jobs {
		if ctx.Err() != nil {
			break
		}
		sem <- struct{}{}
		wg.Go(func() {
			defer func() { <-sem }()
			r.download(j)
		})
	}
	wg.Wait()

	// 5. Remove folders left empty by moves and deletions.
	r.cleanupDirs(dirPaths)

	return r.report, ctx.Err()
}

func (r *run) abs(rel string) string { return filepath.Join(r.dest, filepath.FromSlash(rel)) }

// Steps 1–3 of Run are sequential and touch r.st directly; only the download
// step is concurrent and takes r.mu. The *Locked helpers expect r.mu held (or
// no concurrency).

// failLocked records a per-document error.
func (r *run) failLocked(what string, err error) {
	err = fmt.Errorf("%s: %w", what, err)
	r.log.Error("sync error", "err", err)
	r.report.Errors = append(r.report.Errors, err)
}

func (r *run) fail(what string, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.failLocked(what, err)
}

func (r *run) saveLocked() {
	if err := r.st.save(r.statePath); err != nil {
		r.failLocked("save state", err)
	}
}

func (r *run) trashDoc(id string) {
	ds := r.st.Docs[id]
	for _, f := range ds.Formats {
		if err := r.trashFile(ds.Path + f.Ext()); err != nil && !errors.Is(err, fs.ErrNotExist) {
			r.fail(ds.Path, err)
			return // keep it tracked; retried next run
		}
	}
	delete(r.st.Docs, id)
	r.saveLocked()
	r.report.Trashed++
	r.log.Info("trashed", "path", ds.Path)
}

// trashFile moves Dest/rel to Dest/.trash/rel, never overwriting a previous copy.
func (r *run) trashFile(rel string) error {
	src := r.abs(rel)
	if _, err := os.Lstat(src); err != nil {
		return err
	}
	dst := r.abs(path.Join(trashDir, rel))
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	ext := filepath.Ext(dst)
	base := strings.TrimSuffix(dst, ext)
	stamp := time.Now().Format("2006-01-02 15.04.05")
	for i := 1; exists(dst); i++ {
		dst = fmt.Sprintf("%s (%s)%s", base, stamp, ext)
		if i > 1 {
			dst = fmt.Sprintf("%s (%s %d)%s", base, stamp, i, ext)
		}
	}
	return os.Rename(src, dst)
}

func (r *run) moveDoc(id, newPath string) {
	// If another document still sits at newPath (e.g. two documents swapped
	// names), move it aside first; its own move comes later in this run.
	for otherID, other := range r.st.Docs {
		if otherID != id && other.Path == newPath {
			r.renameDoc(otherID, newPath+" (moving "+shortID(otherID)+")")
		}
	}
	old := r.st.Docs[id].Path
	if r.renameDoc(id, newPath) {
		r.report.Moved++
		r.log.Info("moved", "from", old, "to", newPath)
	}
}

func (r *run) renameDoc(id, newPath string) bool {
	ds := r.st.Docs[id]
	var kept []remarkable.Format
	for _, f := range ds.Formats {
		src, dst := r.abs(ds.Path+f.Ext()), r.abs(newPath+f.Ext())
		if !exists(src) {
			continue // the download step restores it
		}
		err := os.MkdirAll(filepath.Dir(dst), 0o755)
		if err == nil {
			err = os.Rename(src, dst)
		}
		if err != nil {
			r.fail(ds.Path, err)
			return false
		}
		kept = append(kept, f)
	}
	ds.Path, ds.Formats = newPath, kept
	r.saveLocked()
	return true
}

// needed returns the formats to download for a document.
func (r *run) needed(e remarkable.Entry, p string) []remarkable.Format {
	ds := r.st.Docs[e.ID]
	if ds == nil || ds.Modified != e.ModifiedClient {
		return r.formats
	}
	var out []remarkable.Format
	for _, f := range r.formats {
		if !slices.Contains(ds.Formats, f) || !exists(r.abs(p+f.Ext())) {
			out = append(out, f)
		}
	}
	return out
}

func (r *run) download(j job) {
	r.mu.Lock()
	prev := r.st.Docs[j.entry.ID]
	isNew := prev == nil
	r.mu.Unlock()

	var got []remarkable.Format
	for _, f := range j.formats {
		if err := r.fetch(j.entry, j.path+f.Ext(), f); err != nil {
			r.fail(j.path+f.Ext(), err)
			continue
		}
		got = append(got, f)
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	ds := r.st.Docs[j.entry.ID]
	if ds == nil {
		if len(got) == 0 {
			return
		}
		ds = &docState{Path: j.path}
		r.st.Docs[j.entry.ID] = ds
	}
	ds.Name = j.entry.VisibleName
	for _, f := range got {
		if !slices.Contains(ds.Formats, f) {
			ds.Formats = append(ds.Formats, f)
		}
	}
	if len(got) < len(j.formats) {
		r.saveLocked() // keep track of what was written; the rest is retried next run
		return
	}
	ds.Modified = j.entry.ModifiedClient
	r.saveLocked()
	if isNew {
		r.report.Added++
		r.log.Info("added", "path", j.path)
	} else {
		r.report.Updated++
		r.log.Info("updated", "path", j.path)
	}
}

// fetch downloads one file atomically: to a temporary file, then rename.
func (r *run) fetch(e remarkable.Entry, rel string, f remarkable.Format) error {
	dst := r.abs(rel)
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	rc, err := r.s.Client.Download(r.ctx, e.ID, f)
	if err != nil {
		return err
	}
	defer rc.Close()
	tmp, err := os.CreateTemp(filepath.Dir(dst), "."+filepath.Base(dst)+".*"+partExt)
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // no-op after a successful rename
	if _, err := io.Copy(tmp, rc); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o644); err != nil { // CreateTemp uses 0600
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if mt := e.Modified(); !mt.IsZero() {
		_ = os.Chtimes(tmp.Name(), mt, mt)
	}
	return os.Rename(tmp.Name(), dst)
}

// removeStaleParts deletes temporary files left by an interrupted run.
func (r *run) removeStaleParts() {
	_ = filepath.WalkDir(r.dest, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(d.Name(), partExt) {
			_ = os.Remove(p)
		}
		return nil
	})
}

// cleanupDirs removes empty folders that no longer exist on the tablet.
func (r *run) cleanupDirs(dirPaths map[string]string) {
	keep := map[string]bool{r.dest: true}
	for _, d := range dirPaths {
		keep[r.abs(d)] = true
	}
	var dirs []string
	_ = filepath.WalkDir(r.dest, func(p string, d fs.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return nil
		}
		if p == filepath.Join(r.dest, trashDir) {
			return filepath.SkipDir
		}
		if !keep[p] {
			dirs = append(dirs, p)
		}
		return nil
	})
	// Deepest first, so parents emptied by their children go too.
	slices.SortFunc(dirs, func(a, b string) int { return len(b) - len(a) })
	for _, d := range dirs {
		_ = os.Remove(d) // fails harmlessly if not empty
	}
}

func exists(p string) bool {
	_, err := os.Lstat(p)
	return err == nil
}
