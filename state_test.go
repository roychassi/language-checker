package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStateRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data", "state.json")
	now := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	s, note, err := OpenStore(path)
	if err != nil || note != "" {
		t.Fatalf("err=%v note=%q", err, note)
	}
	ep := s.Get(5, "Black Butler S01E20", now)
	ep.Tries, ep.Last, ep.Down = 2, 12, 12
	if err := s.Save(now); err != nil {
		t.Fatal(err)
	}
	s2, _, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	got := s2.Episodes[5]
	if got == nil || got.Tries != 2 || got.Last != 12 || got.Down != 12 || got.Label != "Black Butler S01E20" {
		t.Fatalf("reloaded = %+v", got)
	}
}

func TestSaveLeavesNoTemporaryFiles(t *testing.T) {
	dir := t.TempDir()
	s, _, _ := OpenStore(filepath.Join(dir, "state.json"))
	now := time.Now()
	s.Get(1, "x", now)
	for i := 0; i < 3; i++ {
		s.Episodes[1].Tries = i
		if err := s.Save(now); err != nil {
			t.Fatal(err)
		}
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 || entries[0].Name() != "state.json" {
		t.Fatalf("folder holds %v, want only state.json", entries)
	}
}

func TestUnreadableStateStartsFreshAndKeepsTheOldFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	os.WriteFile(path, []byte("{not json"), 0o600)
	s, note, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Episodes) != 0 || !strings.Contains(note, "state.json.bad") {
		t.Fatalf("episodes=%d note=%q", len(s.Episodes), note)
	}
	if _, err := os.Stat(path + ".bad"); err != nil {
		t.Fatalf("the unreadable file was not kept: %v", err)
	}
}

func TestStateFromANewerVersionIsNotOverwritten(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	os.WriteFile(path, []byte(`{"version": 99, "episodes": {}}`), 0o600)
	if _, note, err := OpenStore(path); err != nil || note == "" {
		t.Fatalf("err=%v note=%q, want the file kept aside", err, note)
	}
	if _, err := os.Stat(path + ".bad"); err != nil {
		t.Fatalf("newer state was not kept: %v", err)
	}
}

func TestOldEpisodesAreForgotten(t *testing.T) {
	now := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	s, _, _ := OpenStore(filepath.Join(t.TempDir(), "state.json"))
	s.Get(1, "old", now.Add(-forgetAfter-time.Hour))
	s.Get(2, "recent", now.Add(-time.Hour))
	if err := s.Save(now); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Episodes[1]; ok {
		t.Error("an episode untouched past the limit was kept")
	}
	if _, ok := s.Episodes[2]; !ok {
		t.Error("a recent episode was forgotten")
	}
}
