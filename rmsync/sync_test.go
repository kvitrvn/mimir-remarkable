package rmsync

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/kvitrvn/mimir-remarkable/internal/faketablet"
	"github.com/kvitrvn/mimir-remarkable/remarkable"
)

const (
	t1 = "2026-11-24T12:49:26.571Z"
	t2 = "2026-11-25T08:00:00Z"
)

type env struct {
	t    *testing.T
	tb   *faketablet.Tablet
	dest string
	s    *Syncer
}

func newEnv(t *testing.T) *env {
	tb := faketablet.New(t)
	dest := t.TempDir()
	return &env{t: t, tb: tb, dest: dest, s: &Syncer{Client: tb.Client(), Dest: dest}}
}

func (e *env) sync() Report {
	e.t.Helper()
	r, err := e.s.Run(context.Background())
	if err != nil {
		e.t.Fatalf("Run: %v", err)
	}
	return r
}

// files lists regular files under dest (state file excluded), slash-separated.
func (e *env) files() []string {
	e.t.Helper()
	var out []string
	filepath.WalkDir(e.dest, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			e.t.Fatal(err)
		}
		rel, _ := filepath.Rel(e.dest, p)
		if !d.IsDir() && rel != stateFile {
			out = append(out, filepath.ToSlash(rel))
		}
		return nil
	})
	slices.Sort(out)
	return out
}

func (e *env) wantFiles(want ...string) {
	e.t.Helper()
	slices.Sort(want)
	if got := e.files(); !slices.Equal(got, want) {
		e.t.Fatalf("files:\n got  %q\n want %q", got, want)
	}
}

func (e *env) content(rel string) string {
	e.t.Helper()
	b, err := os.ReadFile(filepath.Join(e.dest, rel))
	if err != nil {
		e.t.Fatal(err)
	}
	return string(b)
}

func (e *env) wantReport(r Report, want string) {
	e.t.Helper()
	if r.String() != want {
		e.t.Fatalf("report = %q, want %q (errors: %v)", r, want, r.Errors)
	}
}

func (e *env) wantDownloads(want int) {
	e.t.Helper()
	if got := e.tb.Downloads(); got != want {
		e.t.Fatalf("downloads = %d, want %d", got, want)
	}
}

func seed(e *env) {
	e.tb.AddDoc("d1", "Journal ", "", t1)
	e.tb.AddFolder("f1", "Archive", "")
	e.tb.AddFolder("f2", "Work", "")
	e.tb.AddDoc("d2", "Meeting notes", "f2", t1)
}

func TestFirstSyncAndIdempotence(t *testing.T) {
	e := newEnv(t)
	seed(e)
	e.wantReport(e.sync(), "2 added")
	e.wantDownloads(4)
	e.wantFiles("Journal.pdf", "Journal.rmdoc", "Work/Meeting notes.pdf", "Work/Meeting notes.rmdoc")
	if !exists(filepath.Join(e.dest, "Archive")) {
		t.Error("empty tablet folder Archive not mirrored")
	}
	if got, want := e.content("Journal.pdf"), faketablet.Content("d1", t1, remarkable.FormatPDF); got != want {
		t.Errorf("Journal.pdf = %q, want %q", got, want)
	}
	info, _ := os.Stat(filepath.Join(e.dest, "Journal.pdf"))
	if got := info.ModTime().UTC().Format("2006-01-02T15:04:05.000Z"); got != t1 {
		t.Errorf("mtime = %s, want %s", got, t1)
	}

	e.wantReport(e.sync(), "up to date")
	e.wantDownloads(0)
}

func TestModified(t *testing.T) {
	e := newEnv(t)
	seed(e)
	e.sync()
	e.tb.Downloads()

	e.tb.Update("d2", func(en *remarkable.Entry) { en.ModifiedClient = t2 })
	e.wantReport(e.sync(), "1 updated")
	e.wantDownloads(2)
	if got, want := e.content("Work/Meeting notes.rmdoc"), faketablet.Content("d2", t2, remarkable.FormatRMDoc); got != want {
		t.Errorf("content = %q, want %q", got, want)
	}
}

func TestMissingFileRestored(t *testing.T) {
	e := newEnv(t)
	seed(e)
	e.sync()
	e.tb.Downloads()

	os.Remove(filepath.Join(e.dest, "Journal.pdf"))
	e.wantReport(e.sync(), "1 updated")
	e.wantDownloads(1)
	e.wantFiles("Journal.pdf", "Journal.rmdoc", "Work/Meeting notes.pdf", "Work/Meeting notes.rmdoc")
}

func TestRenameAndMove(t *testing.T) {
	e := newEnv(t)
	seed(e)
	e.sync()
	e.tb.Downloads()

	e.tb.Update("d2", func(en *remarkable.Entry) { en.Parent = "f1"; en.VisibleName = "Meeting" })
	e.wantReport(e.sync(), "1 moved")
	e.wantDownloads(0)
	e.wantFiles("Journal.pdf", "Journal.rmdoc", "Archive/Meeting.pdf", "Archive/Meeting.rmdoc")
	if !exists(filepath.Join(e.dest, "Work")) {
		t.Error("Work still exists on the tablet and must be kept even if empty")
	}

	// Folder rename moves its documents.
	e.tb.Update("f1", func(en *remarkable.Entry) { en.VisibleName = "Archive 2" })
	e.wantReport(e.sync(), "1 moved")
	e.wantFiles("Journal.pdf", "Journal.rmdoc", "Archive 2/Meeting.pdf", "Archive 2/Meeting.rmdoc")
	if exists(filepath.Join(e.dest, "Archive")) {
		t.Error("old folder Archive not cleaned up")
	}
}

func TestSwapNames(t *testing.T) {
	e := newEnv(t)
	e.tb.AddDoc("a", "A", "", t1)
	e.tb.AddDoc("b", "B", "", t1)
	e.sync()
	e.tb.Downloads()

	e.tb.Update("a", func(en *remarkable.Entry) { en.VisibleName = "B" })
	e.tb.Update("b", func(en *remarkable.Entry) { en.VisibleName = "A" })
	e.wantReport(e.sync(), "2 moved")
	e.wantDownloads(0)
	if got := e.content("B.pdf"); got != faketablet.Content("a", t1, remarkable.FormatPDF) {
		t.Errorf("B.pdf = %q, want doc a", got)
	}
	if got := e.content("A.pdf"); got != faketablet.Content("b", t1, remarkable.FormatPDF) {
		t.Errorf("A.pdf = %q, want doc b", got)
	}
}

func TestDeleteGoesToTrash(t *testing.T) {
	e := newEnv(t)
	seed(e)
	e.sync()

	e.tb.Remove("d2")
	e.wantReport(e.sync(), "1 trashed")
	e.wantFiles("Journal.pdf", "Journal.rmdoc", ".trash/Work/Meeting notes.pdf", ".trash/Work/Meeting notes.rmdoc")

	// A second document with the same path deleted later does not overwrite the first.
	e.tb.AddDoc("d3", "Meeting notes", "f2", t2)
	e.sync()
	e.tb.Remove("d3")
	e.wantReport(e.sync(), "1 trashed")
	var n int
	for _, f := range e.files() {
		if strings.HasPrefix(f, ".trash/Work/Meeting notes") && strings.HasSuffix(f, ".pdf") {
			n++
		}
	}
	if n != 2 {
		t.Errorf("trash has %d PDFs, want 2: %q", n, e.files())
	}
}

func TestEmptyListingDoesNotTrash(t *testing.T) {
	e := newEnv(t)
	seed(e)
	e.sync()
	for _, id := range []string{"d1", "d2", "f1", "f2"} {
		e.tb.Remove(id)
	}
	e.wantReport(e.sync(), "up to date")
	e.wantFiles("Journal.pdf", "Journal.rmdoc", "Work/Meeting notes.pdf", "Work/Meeting notes.rmdoc")
}

func TestNameCollisionAndSanitize(t *testing.T) {
	e := newEnv(t)
	e.tb.AddDoc("bbbbbbbb-2", "Notes", "", t1)
	e.tb.AddDoc("aaaaaaaa-1", "Notes ", "", t1)
	e.tb.AddDoc("c", "a/b", "", t1)
	e.tb.AddDoc("t", ".trash", "", t1)
	e.wantReport(e.sync(), "4 added")
	e.wantFiles(
		"Notes.pdf", "Notes.rmdoc",
		"Notes (bbbbbbbb).pdf", "Notes (bbbbbbbb).rmdoc",
		"a_b.pdf", "a_b.rmdoc",
		"_.trash.pdf", "_.trash.rmdoc",
	)
	if got := e.content("Notes.pdf"); got != faketablet.Content("aaaaaaaa-1", t1, remarkable.FormatPDF) {
		t.Errorf("Notes.pdf belongs to the wrong document: %q", got)
	}

	// The current owner of the plain name keeps it, even if a smaller ID shows up.
	e.tb.AddDoc("0000", "Notes", "", t1)
	e.wantReport(e.sync(), "1 added")
	if got := e.content("Notes.pdf"); got != faketablet.Content("aaaaaaaa-1", t1, remarkable.FormatPDF) {
		t.Errorf("Notes.pdf changed owner: %q", got)
	}
}

func TestBrokenDownload(t *testing.T) {
	e := newEnv(t)
	seed(e)
	e.tb.Break("d1", true)
	r := e.sync()
	e.wantReport(r, "1 added, 2 errors")
	e.wantFiles("Work/Meeting notes.pdf", "Work/Meeting notes.rmdoc") // no partial file left

	e.tb.Break("d1", false)
	e.tb.Downloads()
	e.wantReport(e.sync(), "1 added")
	e.wantDownloads(2)
}

func TestFormatsAndConcurrency(t *testing.T) {
	e := newEnv(t)
	seed(e)
	e.s.Formats = []remarkable.Format{remarkable.FormatPDF}
	e.s.Concurrency = 4
	e.wantReport(e.sync(), "2 added")
	e.wantFiles("Journal.pdf", "Work/Meeting notes.pdf")

	// Adding a format later fetches only the missing files.
	e.tb.Downloads()
	e.s.Formats = DefaultFormats
	e.wantReport(e.sync(), "2 updated")
	e.wantDownloads(2)
}

func TestSanitize(t *testing.T) {
	for in, want := range map[string]string{
		"Journal ":  "Journal",
		"a/b\\c":    "a_b_c",
		"tab\there": "tab_here",
		"   ":       "untitled",
		"..":        "_..",
		"Café ☕":    "Café ☕",
	} {
		if got := sanitize(in); got != want {
			t.Errorf("sanitize(%q) = %q, want %q", in, got, want)
		}
	}
	long := strings.Repeat("é", 150) // 300 bytes
	if got := sanitize(long); len(got) > maxNameBytes || !strings.HasPrefix(long, got) {
		t.Errorf("long name not truncated on a rune boundary: %d bytes", len(got))
	}
}
