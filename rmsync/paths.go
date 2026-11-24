package rmsync

import (
	"path"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/kvitrvn/mimir-remarkable/remarkable"
)

// Reserved names at the root of the destination.
const (
	trashDir  = ".trash"
	stateFile = ".mimir-state.json"
	partExt   = ".mimir-part"
)

const maxNameBytes = 200 // leaves room for suffixes and extensions under NAME_MAX (255)

// sanitize turns a tablet name into a safe single path component.
func sanitize(name string) string {
	name = strings.Map(func(r rune) rune {
		if r == '/' || r == '\\' || unicode.IsControl(r) {
			return '_'
		}
		return r
	}, name)
	name = strings.TrimSpace(name)
	for len(name) > maxNameBytes {
		_, size := utf8.DecodeLastRuneInString(name)
		name = strings.TrimSpace(name[:len(name)-size])
	}
	switch name {
	case "":
		return "untitled"
	case ".", "..":
		return "_" + name
	}
	return name
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// layout assigns every tablet entry a relative, slash-separated local path
// (without extension for documents). Names that collide inside a folder
// after sanitization get a short-ID suffix; a document that already owns the
// plain name (per prev) keeps it, otherwise the smallest ID does.
func layout(nodes []remarkable.Node, prev map[string]*docState) (docs, dirs map[string]string) {
	children := map[string][]remarkable.Entry{}
	for _, n := range nodes {
		children[n.Parent] = append(children[n.Parent], n.Entry)
	}
	docs, dirs = map[string]string{}, map[string]string{}

	var place func(parentID, dir string)
	place = func(parentID, dir string) {
		groups := map[string][]remarkable.Entry{} // key: kind + name
		for _, e := range children[parentID] {
			name := sanitize(e.VisibleName)
			if dir == "" && (name == trashDir || name == stateFile) {
				name = "_" + name
			}
			kind := "d:"
			if e.IsFolder() {
				kind = "f:"
			}
			groups[kind+name] = append(groups[kind+name], e)
		}
		for key, group := range groups {
			name := key[2:]
			plain := path.Join(dir, name)
			slices.SortFunc(group, func(a, b remarkable.Entry) int {
				// Previous owner of the plain name first, then by ID.
				ao, bo := owns(prev, a.ID, plain), owns(prev, b.ID, plain)
				if ao != bo {
					if ao {
						return -1
					}
					return 1
				}
				return strings.Compare(a.ID, b.ID)
			})
			for i, e := range group {
				p := plain
				if i > 0 {
					p = path.Join(dir, name+" ("+shortID(e.ID)+")")
				}
				if e.IsFolder() {
					dirs[e.ID] = p
					place(e.ID, p)
				} else {
					docs[e.ID] = p
				}
			}
		}
	}
	place("", "")
	return docs, dirs
}

func owns(prev map[string]*docState, id, p string) bool {
	s, ok := prev[id]
	return ok && s.Path == p
}
