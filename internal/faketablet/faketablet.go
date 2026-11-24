// Package faketablet is an in-memory reMarkable USB web interface for tests.
package faketablet

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/kvitrvn/mimir-remarkable/remarkable"
)

// Tablet holds the fake tree. Mutate it with the helper methods between syncs.
type Tablet struct {
	Server *httptest.Server

	mu        sync.Mutex
	entries   map[string]remarkable.Entry
	order     []string        // insertion order, as the tablet lists it
	broken    map[string]bool // downloads of these IDs are cut short
	downloads map[string]int  // "id/format" -> count
}

// New starts a fake tablet; it is closed when the test ends.
func New(t testing.TB) *Tablet {
	tb := &Tablet{
		entries:   map[string]remarkable.Entry{},
		broken:    map[string]bool{},
		downloads: map[string]int{},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "<html>reMarkable</html>")
	})
	mux.HandleFunc("POST /documents/{id...}", tb.list)
	mux.HandleFunc("GET /download/{id}/{format}", tb.download)
	tb.Server = httptest.NewServer(mux)
	t.Cleanup(tb.Server.Close)
	return tb
}

// Client returns a client pointed at the fake tablet.
func (tb *Tablet) Client() *remarkable.Client { return remarkable.NewClient(tb.Server.URL) }

// AddFolder adds a folder under parent ("" for root).
func (tb *Tablet) AddFolder(id, name, parent string) {
	tb.put(remarkable.Entry{ID: id, VisibleName: name, Parent: parent, Type: remarkable.TypeCollection, ModifiedClient: "2026-01-01T00:00:00Z"})
}

// AddDoc adds a notebook under parent with the given modification time.
func (tb *Tablet) AddDoc(id, name, parent, modified string) {
	tb.put(remarkable.Entry{ID: id, VisibleName: name, Parent: parent, Type: remarkable.TypeDocument, ModifiedClient: modified, FileType: "notebook"})
}

func (tb *Tablet) put(e remarkable.Entry) {
	tb.mu.Lock()
	defer tb.mu.Unlock()
	if _, ok := tb.entries[e.ID]; !ok {
		tb.order = append(tb.order, e.ID)
	}
	tb.entries[e.ID] = e
}

// Update changes an existing entry.
func (tb *Tablet) Update(id string, fn func(*remarkable.Entry)) {
	tb.mu.Lock()
	defer tb.mu.Unlock()
	e := tb.entries[id]
	fn(&e)
	tb.entries[id] = e
}

// Remove deletes an entry.
func (tb *Tablet) Remove(id string) {
	tb.mu.Lock()
	defer tb.mu.Unlock()
	delete(tb.entries, id)
	tb.order = slices.DeleteFunc(tb.order, func(s string) bool { return s == id })
}

// Break makes downloads of id fail mid-transfer (or succeed again with false).
func (tb *Tablet) Break(id string, broken bool) {
	tb.mu.Lock()
	defer tb.mu.Unlock()
	tb.broken[id] = broken
}

// Downloads returns the total number of downloads served, and resets the counter.
func (tb *Tablet) Downloads() int {
	tb.mu.Lock()
	defer tb.mu.Unlock()
	n := 0
	for _, c := range tb.downloads {
		n += c
	}
	clear(tb.downloads)
	return n
}

// Content is the body served for a document in a format.
func Content(id, modified string, f remarkable.Format) string {
	return fmt.Sprintf("%s|%s|%s", f, id, modified)
}

func (tb *Tablet) list(w http.ResponseWriter, r *http.Request) {
	parent := strings.TrimSuffix(r.PathValue("id"), "/")
	tb.mu.Lock()
	out := []remarkable.Entry{}
	for _, id := range tb.order {
		if e := tb.entries[id]; e.Parent == parent {
			out = append(out, e)
		}
	}
	tb.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}

func (tb *Tablet) download(w http.ResponseWriter, r *http.Request) {
	id, format := r.PathValue("id"), remarkable.Format(r.PathValue("format"))
	tb.mu.Lock()
	e, ok := tb.entries[id]
	broken := tb.broken[id]
	tb.downloads[id+"/"+string(format)]++
	tb.mu.Unlock()
	if !ok || e.IsFolder() {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	body := Content(id, e.ModifiedClient, format)
	if broken {
		// Announce more than we send: the client sees an unexpected EOF.
		w.Header().Set("Content-Length", fmt.Sprint(len(body)+100))
	}
	fmt.Fprint(w, body)
}
