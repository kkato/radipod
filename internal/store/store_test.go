package store

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func testEpisode() Episode {
	start := time.Date(2026, 10, 16, 1, 0, 0, 0, time.UTC)
	return Episode{ID: "LFR_test", Station: "LFR", Title: "test", Start: start, End: start.Add(time.Minute)}
}

func TestAtomicVisibilityAndDeduplication(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	e := testEpisode()
	saved, err := s.Save(e, func(path string) (float64, error) {
		if err := os.WriteFile(path, []byte("audio"), 0600); err != nil {
			return 0, err
		}
		es, err := s.List()
		if err != nil || len(es) != 0 {
			t.Fatalf("pending episode visible: %v %v", es, err)
		}
		return 60, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if saved.Size != 5 {
		t.Fatal(saved)
	}
	es, err := s.List()
	if err != nil || len(es) != 1 {
		t.Fatalf("%v %v", es, err)
	}
	_, err = s.Save(e, func(string) (float64, error) { t.Fatal("duplicate downloaded"); return 0, nil })
	if !errors.Is(err, ErrExists) {
		t.Fatal(err)
	}
}

func TestFailedWriteLeavesNoEpisode(t *testing.T) {
	s, _ := New(t.TempDir())
	_, err := s.Save(testEpisode(), func(path string) (float64, error) {
		_ = os.WriteFile(path, []byte("partial"), 0600)
		return 0, errors.New("network disconnected")
	})
	if err == nil {
		t.Fatal("missing failure")
	}
	entries, err := os.ReadDir(s.Dir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("leftover files: %v %v", entries, err)
	}
}

func TestTraversalAndExternalSymlinkRejected(t *testing.T) {
	s, _ := New(t.TempDir())
	if _, err := s.OpenAudio("../secret"); err == nil {
		t.Fatal("accepted traversal")
	}
	other, _ := New(t.TempDir())
	e := testEpisode()
	_, err := other.Save(e, func(path string) (float64, error) { return 60, os.WriteFile(path, []byte("secret"), 0600) })
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(other.Dir, e.ID), filepath.Join(s.Dir, e.ID)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.OpenAudio(e.ID); err == nil {
		t.Fatal("followed external symlink")
	}
}
