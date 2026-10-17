package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/kkato/radipod/internal/config"
	"github.com/kkato/radipod/internal/radiko"
	"github.com/kkato/radipod/internal/store"
)

type fakeAPI struct {
	programs  map[string][]radiko.Program
	dates     []string
	authCalls int
}

func (a *fakeAPI) Authorize(context.Context) (radiko.Session, error) {
	a.authCalls++
	return radiko.Session{Token: fmt.Sprint(a.authCalls), Area: "JP13"}, nil
}
func (a *fakeAPI) Stations(context.Context, string) ([]radiko.Station, error) {
	return []radiko.Station{{ID: "LFR", TimeFree: "1"}}, nil
}
func (a *fakeAPI) Programs(_ context.Context, area, station string, date time.Time) ([]radiko.Program, error) {
	key := date.Format("2006-01-02")
	a.dates = append(a.dates, key)
	return a.programs[key], nil
}
func (a *fakeAPI) Playlists(context.Context, string) ([]string, error) {
	return []string{"https://example.com/tf"}, nil
}

type fakeDownloader struct {
	calls  []string
	tokens []string
	failID string
}

func (r *fakeDownloader) Download(ctx context.Context, s radiko.Session, p radiko.Program, _ []string, path string) (float64, error) {
	r.calls = append(r.calls, EpisodeID(p))
	r.tokens = append(r.tokens, s.Token)
	if r.failID == EpisodeID(p) {
		return 0, errors.New("temporary network failure")
	}
	return p.End.Sub(p.Start).Seconds(), os.WriteFile(path, []byte("audio"), 0600)
}

func TestSyncCatchesUpWithoutDuplicatesAndRetriesFailures(t *testing.T) {
	now, _ := radiko.ParseTime("20261017020000")
	older := radiko.Program{Station: "LFR", Title: "番組 A", Start: now.Add(-24 * time.Hour), End: now.Add(-23 * time.Hour)}
	recent := radiko.Program{Station: "LFR", Title: "番組 B", Start: now.Add(-3 * time.Hour), End: now.Add(-2 * time.Hour)}
	ongoing := radiko.Program{Station: "LFR", Title: "番組 C", Start: now.Add(-time.Hour), End: now.Add(time.Hour)}
	blocked := recent
	blocked.Title = "番組 restricted"
	blocked.Unavailable = true
	a := &fakeAPI{programs: map[string][]radiko.Program{"2026-10-15": {older}, "2026-10-16": {recent, ongoing, blocked}}}
	r := &fakeDownloader{failID: EpisodeID(older)}
	s, _ := store.New(t.TempDir())
	e := &Engine{API: a, Store: s, Downloader: r, Timeout: time.Minute}
	cfg := config.Config{LookbackDays: 2, Rules: []config.Rule{{Station: "LFR", Match: "番組"}, {Station: "LFR", Match: "番組 A"}}}
	result, err := e.Sync(context.Background(), cfg, now, false)
	if err == nil || result.Saved != 1 || len(r.calls) != 2 {
		t.Fatalf("%+v %v %v", result, err, r.calls)
	}
	if strings.Join(a.dates, ",") != "2026-10-15,2026-10-16" {
		t.Fatal(a.dates)
	}
	if r.calls[0] != EpisodeID(older) || r.calls[1] != EpisodeID(recent) {
		t.Fatal(r.calls)
	}
	if r.tokens[0] == r.tokens[1] {
		t.Fatal("reused authentication token")
	}
	r.failID = ""
	result, err = e.Sync(context.Background(), cfg, now, false)
	if err != nil || result.Saved != 1 || result.Existing != 1 || len(r.calls) != 3 {
		t.Fatalf("%+v %v %v", result, err, r.calls)
	}
	result, err = e.Sync(context.Background(), cfg, now, false)
	if err != nil || result.Existing != 2 || len(r.calls) != 3 {
		t.Fatalf("downloaded again: %+v %v", result, err)
	}
}

func TestDryRunAndNoMatchDoNotDownload(t *testing.T) {
	now, _ := radiko.ParseTime("20261017120000")
	p := radiko.Program{Station: "LFR", Title: "MY SHOW", Start: now.Add(-3 * time.Hour), End: now.Add(-2 * time.Hour)}
	a := &fakeAPI{programs: map[string][]radiko.Program{"2026-10-17": {p}}}
	s, _ := store.New(t.TempDir())
	e := &Engine{API: a, Store: s} // no downloader: dry-run must work without FFmpeg
	cfg := config.Config{LookbackDays: 1, Rules: []config.Rule{{Station: "LFR", Match: "my show"}}}
	result, err := e.Sync(context.Background(), cfg, now, true)
	if err != nil || result.Planned != 1 || a.authCalls != 1 {
		t.Fatalf("%+v %v", result, err)
	}
	items, _ := s.List()
	if len(items) != 0 {
		t.Fatal("dry-run saved audio")
	}
	cfg.Rules[0].Match = "no match"
	result, err = e.Sync(context.Background(), cfg, now, true)
	if err != nil || result.Planned != 0 {
		t.Fatalf("no-match should succeed: %+v %v", result, err)
	}
	cfg.Rules = nil
	count := a.authCalls
	_, err = e.Sync(context.Background(), cfg, now, false)
	if err != nil || a.authCalls != count {
		t.Fatal("empty rules made network requests")
	}
}

func TestWatchRetriesAndStopsOnCancellation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	runs, reports := 0, 0
	Watch(ctx, time.Millisecond, func(ctx context.Context) error {
		runs++
		if runs == 1 {
			return errors.New("network down")
		}
		cancel()
		return nil
	}, func(error) { reports++ })
	if runs != 2 || reports != 1 {
		t.Fatalf("runs=%d reports=%d", runs, reports)
	}
}

func TestCancelledRecordDoesNotContactRadiko(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	a := &fakeAPI{}
	e := &Engine{API: a}
	_, err := e.Record(ctx, []radiko.Program{{Station: "LFR"}}, false)
	if !errors.Is(err, context.Canceled) || a.authCalls != 0 {
		t.Fatal(err)
	}
}
