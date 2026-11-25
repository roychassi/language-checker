package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const (
	stateVersion = 1

	// forgetAfter is how long an episode that needs nothing more is kept in
	// the state file. A refusal a long time later starts a fresh count.
	forgetAfter = 60 * 24 * time.Hour

	// maxSeen caps the releases remembered per episode while in dry run.
	maxSeen = 50
)

// Episode is what the plugin remembers about one slot (a movie, or one
// episode of a show) between checks and restarts.
type Episode struct {
	Label   string    `json:"label"`
	Tries   int       `json:"tries"`             // releases refused for audio so far
	Last    int64     `json:"last,omitempty"`    // the release counted most recently
	Down    int64     `json:"down,omitempty"`    // the release this plugin turned down last
	Seen    []int64   `json:"seen,omitempty"`    // releases already reported while in dry run
	GaveUp  bool      `json:"gave_up,omitempty"` // the owner was told the limit was reached
	Updated time.Time `json:"updated"`
}

// Store keeps the episodes in one JSON file in the plugin's data folder.
type Store struct {
	path     string
	Episodes map[int64]*Episode
	written  []byte // what is on disk, so an unchanged state is not rewritten
}

type stateFile struct {
	Version  int                `json:"version"`
	Episodes map[int64]*Episode `json:"episodes"`
}

// OpenStore reads the state file. A missing file is a fresh start. A file
// that cannot be read is moved aside as <name>.bad and also treated as a
// fresh start, because the plugin must come up either way; the second return
// value says what happened, for the log.
func OpenStore(path string) (*Store, string, error) {
	s := &Store{path: path, Episodes: map[int64]*Episode{}}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, "", nil
	}
	if err != nil {
		return nil, "", err
	}
	var f stateFile
	if err := json.Unmarshal(data, &f); err != nil || f.Version > stateVersion {
		bad := path + ".bad"
		if rerr := os.Rename(path, bad); rerr != nil {
			return nil, "", fmt.Errorf("state file is unreadable and could not be moved aside: %w", rerr)
		}
		return s, "state file could not be read; started fresh and kept the old one as " + filepath.Base(bad), nil
	}
	if f.Episodes != nil {
		s.Episodes = f.Episodes
	}
	s.written = data
	return s, "", nil
}

// Get returns the record for a slot, creating it when there is none.
func (s *Store) Get(slot int64, label string, now time.Time) *Episode {
	ep := s.Episodes[slot]
	if ep == nil {
		ep = &Episode{Updated: now}
		s.Episodes[slot] = ep
	}
	if label != "" {
		ep.Label = label
	}
	return ep
}

// Save forgets stale episodes, then writes the file: to a temporary file in
// the same folder first and renamed into place, so a stop in the middle of a
// write cannot leave half a file.
func (s *Store) Save(now time.Time) error {
	for id, ep := range s.Episodes {
		if now.Sub(ep.Updated) > forgetAfter {
			delete(s.Episodes, id)
		}
	}
	data, err := json.MarshalIndent(stateFile{Version: stateVersion, Episodes: s.Episodes}, "", "  ")
	if err != nil {
		return err
	}
	if bytes.Equal(data, s.written) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".state-*.tmp")
	if err != nil {
		return err
	}
	_, werr := tmp.Write(data)
	cerr := tmp.Close()
	if err := errors.Join(werr, cerr); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	if err := os.Rename(tmp.Name(), s.path); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	s.written = data
	return nil
}
