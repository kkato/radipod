// Package store publishes each episode with a single directory rename.
package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

var ErrExists = errors.New("episode already exists")
var idPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$`)

type Episode struct {
	ID          string    `json:"id"`
	Station     string    `json:"station"`
	Title       string    `json:"title"`
	Description string    `json:"description"`
	Start       time.Time `json:"start"`
	End         time.Time `json:"end"`
	Duration    float64   `json:"duration_seconds"`
	Size        int64     `json:"size_bytes"`
	CreatedAt   time.Time `json:"created_at"`
}

type Store struct{ Dir string }

func New(dir string) (*Store, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(abs, "episodes")
	if err := os.MkdirAll(path, 0700); err != nil {
		return nil, err
	}
	return &Store{Dir: path}, nil
}

func ValidID(id string) bool { return idPattern.MatchString(id) }

func (s *Store) Load(id string) (Episode, error) {
	var e Episode
	if !ValidID(id) {
		return e, fmt.Errorf("invalid episode ID")
	}
	root, err := os.OpenRoot(s.Dir)
	if err != nil {
		return e, err
	}
	defer root.Close()
	f, err := root.Open(filepath.Join(id, "episode.json"))
	if err != nil {
		return e, err
	}
	defer f.Close()
	if err := json.NewDecoder(io.LimitReader(f, 1<<20)).Decode(&e); err != nil {
		return e, err
	}
	if e.ID != id || e.Title == "" || !e.End.After(e.Start) || e.Duration <= 0 || e.Size <= 0 {
		return e, fmt.Errorf("invalid metadata for %s", id)
	}
	info, err := root.Stat(filepath.Join(id, "audio.m4a"))
	if err != nil {
		return e, err
	}
	if !info.Mode().IsRegular() || info.Size() != e.Size {
		return e, fmt.Errorf("invalid audio file for %s", id)
	}
	return e, nil
}

func (s *Store) List() ([]Episode, error) {
	entries, err := os.ReadDir(s.Dir)
	if err != nil {
		return nil, err
	}
	var episodes []Episode
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".") || !entry.IsDir() {
			continue
		}
		e, err := s.Load(entry.Name())
		if err != nil {
			return nil, fmt.Errorf("load %s: %w", entry.Name(), err)
		}
		episodes = append(episodes, e)
	}
	sort.Slice(episodes, func(i, j int) bool {
		if episodes[i].Start.Equal(episodes[j].Start) {
			return episodes[i].ID < episodes[j].ID
		}
		return episodes[i].Start.After(episodes[j].Start)
	})
	return episodes, nil
}

// Save's callback writes a complete M4A and returns its probed duration.
// Pending directories are invisible to readers. Failed writes are removed.
func (s *Store) Save(e Episode, writeAudio func(path string) (float64, error)) (Episode, error) {
	if !ValidID(e.ID) || e.Title == "" || !e.End.After(e.Start) {
		return e, fmt.Errorf("invalid episode")
	}
	if _, err := s.Load(e.ID); err == nil {
		return e, ErrExists
	} else if !errors.Is(err, os.ErrNotExist) {
		return e, err
	}
	tmp, err := os.MkdirTemp(s.Dir, ".pending-")
	if err != nil {
		return e, err
	}
	defer os.RemoveAll(tmp)
	e.Duration, err = writeAudio(filepath.Join(tmp, "audio.m4a"))
	if err != nil {
		return e, err
	}
	if e.Duration <= 0 {
		return e, fmt.Errorf("audio duration must be positive")
	}
	audio, err := os.OpenFile(filepath.Join(tmp, "audio.m4a"), os.O_RDWR, 0)
	if err != nil {
		return e, err
	}
	info, statErr := audio.Stat()
	syncErr := audio.Sync()
	closeErr := audio.Close()
	if err := errors.Join(statErr, syncErr, closeErr); err != nil {
		return e, err
	}
	if !info.Mode().IsRegular() || info.Size() == 0 {
		return e, fmt.Errorf("audio file is empty or not regular")
	}
	e.Size = info.Size()
	e.CreatedAt = time.Now().UTC()
	body, err := json.MarshalIndent(e, "", "  ")
	if err != nil {
		return e, err
	}
	f, err := os.OpenFile(filepath.Join(tmp, "episode.json"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return e, err
	}
	_, writeErr := f.Write(append(body, '\n'))
	syncErr = f.Sync()
	closeErr = f.Close()
	if err := errors.Join(writeErr, syncErr, closeErr); err != nil {
		return e, err
	}
	if err := os.Rename(tmp, filepath.Join(s.Dir, e.ID)); err != nil {
		if _, loadErr := s.Load(e.ID); loadErr == nil {
			return e, ErrExists
		}
		return e, err
	}
	return e, nil
}

func (s *Store) OpenAudio(id string) (*os.File, error) {
	if _, err := s.Load(id); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(s.Dir)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	return root.Open(filepath.Join(id, "audio.m4a"))
}
