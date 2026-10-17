package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/kkato/radipod/internal/radiko"
	"github.com/kkato/radipod/internal/store"
)

type API interface {
	Authorize(context.Context) (radiko.Session, error)
	Stations(context.Context, string) ([]radiko.Station, error)
	Programs(context.Context, string, string, time.Time) ([]radiko.Program, error)
	Playlists(context.Context, string) ([]string, error)
}

type Downloader interface {
	Download(context.Context, radiko.Session, radiko.Program, []string, string) (float64, error)
}

type Engine struct {
	API        API
	Store      *store.Store
	Downloader Downloader
	Timeout    time.Duration
	Log        func(string)
}

type Result struct{ Saved, Existing, Planned int }

func EpisodeID(p radiko.Program) string {
	return p.Station + "_" + p.Start.In(radiko.JST).Format(radiko.TimeLayout) + "_" + p.End.In(radiko.JST).Format(radiko.TimeLayout)
}

func (e *Engine) log(format string, args ...any) {
	if e.Log != nil {
		e.Log(fmt.Sprintf(format, args...))
	}
}

func (e *Engine) Record(ctx context.Context, programs []radiko.Program, dryRun bool) (Result, error) {
	var result Result
	var failures []error
	for _, p := range programs {
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		id := EpisodeID(p)
		if _, err := e.Store.Load(id); err == nil {
			result.Existing++
			e.log("already saved: %s", id)
			continue
		} else if !errors.Is(err, os.ErrNotExist) {
			failures = append(failures, err)
			continue
		}
		if dryRun {
			result.Planned++
			e.log("would save: %s %s", id, p.Title)
			continue
		}
		if e.Downloader == nil {
			return result, fmt.Errorf("recorder is not configured")
		}
		if e.Timeout <= 0 {
			return result, fmt.Errorf("recording timeout must be positive")
		}
		recordCtx, cancel := context.WithTimeout(ctx, e.Timeout)
		// Refresh credentials for every episode so long batches don't reuse an old token.
		session, err := e.API.Authorize(recordCtx)
		var playlists []string
		if err == nil {
			playlists, err = e.API.Playlists(recordCtx, p.Station)
		}
		var saved store.Episode
		if err == nil {
			e.log("recording: %s %s", p.Station, p.Title)
			saved, err = e.Store.Save(store.Episode{ID: id, Station: p.Station, Title: p.Title, Description: p.Description, Start: p.Start, End: p.End}, func(path string) (float64, error) {
				return e.Downloader.Download(recordCtx, session, p, playlists, path)
			})
		}
		cancel()
		if errors.Is(err, store.ErrExists) {
			result.Existing++
			continue
		}
		if err != nil {
			failures = append(failures, fmt.Errorf("%s: %w", id, err))
			continue
		}
		result.Saved++
		e.log("saved: %s (%.1fs, %d bytes)", saved.ID, saved.Duration, saved.Size)
	}
	return result, errors.Join(failures...)
}
