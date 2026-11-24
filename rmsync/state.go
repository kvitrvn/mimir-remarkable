package rmsync

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/kvitrvn/mimir-remarkable/remarkable"
)

const stateVersion = 1

// state is what was synced last time, stored in Dest/.mimir-state.json.
type state struct {
	Version int                  `json:"version"`
	Docs    map[string]*docState `json:"docs"`
}

type docState struct {
	Name string `json:"name"` // tablet name, informational
	// Modified is the tablet's ModifiedClient when the files were last fully
	// downloaded; empty if a download failed partway.
	Modified string `json:"modified"`
	// Path is the slash-separated path relative to Dest, without extension.
	Path string `json:"path"`
	// Formats are the files present locally for this document.
	Formats []remarkable.Format `json:"formats"`
}

func loadState(file string) (*state, error) {
	st := &state{Version: stateVersion, Docs: map[string]*docState{}}
	b, err := os.ReadFile(file)
	if errors.Is(err, fs.ErrNotExist) {
		return st, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, st); err != nil {
		return nil, fmt.Errorf("parse %s: %w", file, err)
	}
	if st.Version != stateVersion {
		return nil, fmt.Errorf("%s: unsupported state version %d", file, st.Version)
	}
	if st.Docs == nil {
		st.Docs = map[string]*docState{}
	}
	return st, nil
}

func (st *state) save(file string) error {
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(file, b)
}

func writeFileAtomic(file string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(file), "."+filepath.Base(file)+".*"+partExt)
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // no-op after a successful rename
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), file)
}
