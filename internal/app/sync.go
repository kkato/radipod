package app

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/kkato/radipod/internal/config"
	"github.com/kkato/radipod/internal/radiko"
)

// Sync scans a bounded number of broadcast days and records oldest matches first.
// A failure for one station or episode does not discard the other stations' work.
func (e *Engine) Sync(ctx context.Context, cfg config.Config, now time.Time, dryRun bool) (Result, error) {
	if len(cfg.Rules) == 0 {
		e.log("no recording rules configured")
		return Result{}, nil
	}
	session, err := e.API.Authorize(ctx)
	if err != nil {
		return Result{}, err
	}
	stations, err := e.API.Stations(ctx, session.Area)
	if err != nil {
		return Result{}, err
	}
	available := map[string]bool{}
	for _, s := range stations {
		available[s.ID] = s.TimeFree == "1"
	}
	grouped := map[string][]string{}
	for _, r := range cfg.Rules {
		grouped[r.Station] = append(grouped[r.Station], strings.ToLower(r.Match))
	}
	stationIDs := make([]string, 0, len(grouped))
	for id := range grouped {
		stationIDs = append(stationIDs, id)
	}
	sort.Strings(stationIDs)
	var failures []error
	selected := map[string]radiko.Program{}
	for _, station := range stationIDs {
		if !available[station] {
			failures = append(failures, fmt.Errorf("%s is not a time-free station in %s", station, session.Area))
			continue
		}
		for day := cfg.LookbackDays - 1; day >= 0; day-- {
			if ctx.Err() != nil {
				return Result{}, ctx.Err()
			}
			date := radiko.BroadcastDate(now).AddDate(0, 0, -day)
			programs, err := e.API.Programs(ctx, session.Area, station, date)
			if err != nil {
				failures = append(failures, fmt.Errorf("%s %s: %w", station, date.Format("2006-01-02"), err))
				continue
			}
			for _, p := range programs {
				if p.End.After(now.Add(-3*time.Minute)) || p.Start.Before(now.Add(-7*24*time.Hour)) {
					continue
				}
				matches := false
				for _, pattern := range grouped[station] {
					if strings.Contains(strings.ToLower(p.Title), pattern) {
						matches = true
						break
					}
				}
				if !matches {
					continue
				}
				if p.Unavailable {
					e.log("unavailable: %s %s", p.Station, p.Title)
					continue
				}
				selected[EpisodeID(p)] = p
			}
		}
	}
	programs := make([]radiko.Program, 0, len(selected))
	for _, p := range selected {
		programs = append(programs, p)
	}
	sort.Slice(programs, func(i, j int) bool {
		if programs[i].Start.Equal(programs[j].Start) {
			return EpisodeID(programs[i]) < EpisodeID(programs[j])
		}
		return programs[i].Start.Before(programs[j].Start)
	})
	result, err := e.Record(ctx, programs, dryRun)
	if err != nil {
		failures = append(failures, err)
	}
	e.log("sync finished: saved=%d existing=%d planned=%d errors=%d", result.Saved, result.Existing, result.Planned, len(failures))
	return result, errors.Join(failures...)
}

// Watch runs immediately, then waits after each pass, preventing overlapping jobs.
func Watch(ctx context.Context, interval time.Duration, run func(context.Context) error, report func(error)) {
	for ctx.Err() == nil {
		if err := run(ctx); err != nil && ctx.Err() == nil && report != nil {
			report(err)
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}
