package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/kkato/radipod/internal/app"
	"github.com/kkato/radipod/internal/feed"
	"github.com/kkato/radipod/internal/radiko"
	"github.com/kkato/radipod/internal/recorder"
	"github.com/kkato/radipod/internal/server"
	"github.com/kkato/radipod/internal/store"
)

const usage = `radipod — record radio and serve a personal podcast

Commands:
  run        Serve and auto-record: -config config.local.json
  sync       Fetch configured matches once: -config config.local.json [-dry-run]
  stations   List time-free stations available in your connection's area
  programs   List programs: -station LFR -date yesterday
  record     Save a program: -station LFR -start YYYYMMDDhhmmss [-end ...]
             Or save finished matches: -station LFR -date yesterday -match WORD
  serve      Serve RSS and audio: -base-url http://SERVER:8080 [-listen :8080]
  feed       Print RSS: -base-url http://SERVER:8080

Use radipod COMMAND -h for options. Times are JST.
Audio is saved as M4A. ffmpeg and ffprobe are required for recording.
This version supports in-area time-free recording, without premium login.`

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := run(ctx, os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "radipod:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, out, errOut io.Writer) error {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		fmt.Fprintln(out, usage)
		return nil
	}
	command := args[0]
	if command == "run" || command == "sync" {
		return runConfigured(ctx, command, args[1:], out, errOut)
	}
	switch command {
	case "stations", "programs", "record", "serve", "feed":
	default:
		return fmt.Errorf("unknown command %q; use -h", command)
	}
	f := flag.NewFlagSet(command, flag.ContinueOnError)
	f.SetOutput(errOut)
	var station, start, end, title, match, date, dataDir, baseURL, listen, imageURL string
	var timeout time.Duration
	switch command {
	case "programs", "record":
		f.StringVar(&station, "station", "", "radiko station ID")
		f.StringVar(&date, "date", "", "broadcast date: YYYY-MM-DD, today, or yesterday (05:00 boundary)")
	}
	if command == "record" {
		f.StringVar(&start, "start", "", "start: YYYYMMDDhhmmss, JST")
		f.StringVar(&end, "end", "", "optional end: YYYYMMDDhhmmss, JST; defaults to program end")
		f.StringVar(&title, "title", "", "optional title override for a single recording")
		f.StringVar(&match, "match", "", "case-insensitive title substring; requires -date")
		f.DurationVar(&timeout, "timeout", 30*time.Minute, "timeout per recording")
	}
	if command == "record" || command == "serve" || command == "feed" {
		f.StringVar(&dataDir, "data", "data", "storage directory")
	}
	if command == "serve" || command == "feed" {
		f.StringVar(&baseURL, "base-url", "", "HTTP(S) origin reachable from your phone, e.g. http://192.168.1.10:8080")
		f.StringVar(&title, "title", "radipod", "podcast title")
		f.StringVar(&imageURL, "image-url", "", "optional HTTP(S) URL for podcast artwork")
	}
	if command == "serve" {
		f.StringVar(&listen, "listen", "127.0.0.1:8080", "HTTP listen address")
	}
	if err := f.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if f.NArg() != 0 {
		return fmt.Errorf("unexpected arguments: %s", strings.Join(f.Args(), " "))
	}
	if command == "serve" || command == "feed" {
		if _, err := feed.ValidateBaseURL(baseURL); err != nil {
			return err
		}
		s, err := store.New(dataDir)
		if err != nil {
			return err
		}
		if command == "feed" {
			episodes, err := s.List()
			if err != nil {
				return err
			}
			b, err := feed.Generate(episodes, baseURL, title, imageURL)
			if err != nil {
				return err
			}
			_, err = out.Write(b)
			return err
		}
		h, err := server.New(s, server.Config{BaseURL: baseURL, Title: title, ImageURL: imageURL, User: os.Getenv("RADIPOD_HTTP_USER"), Password: os.Getenv("RADIPOD_HTTP_PASSWORD")})
		if err != nil {
			return err
		}
		return serve(ctx, h, listen, baseURL, errOut)
	}
	var parsedStart time.Time
	var dateValue time.Time
	var err error
	if command != "stations" {
		if err := radiko.ValidateStation(station); err != nil {
			return err
		}
		if command == "programs" {
			if date == "" {
				date = "today"
			}
			dateValue, err = parseDate(date, time.Now())
			if err != nil {
				return err
			}
		} else {
			if timeout <= 0 {
				return fmt.Errorf("timeout must be positive")
			}
			if start != "" {
				if date != "" || match != "" {
					return fmt.Errorf("use -start or -date with -match, not both")
				}
				parsedStart, err = radiko.ParseTime(start)
				if err != nil {
					return err
				}
				dateValue = radiko.BroadcastDate(parsedStart)
				if end != "" {
					t, err := radiko.ParseTime(end)
					if err != nil {
						return err
					}
					if !t.After(parsedStart) {
						return fmt.Errorf("end must be after start")
					}
				}
			} else {
				if date == "" || strings.TrimSpace(match) == "" || end != "" || title != "" {
					return fmt.Errorf("use -start [-end] [-title], or -date with -match")
				}
				dateValue, err = parseDate(date, time.Now())
				if err != nil {
					return err
				}
			}
		}
	}
	c := radiko.NewClient()
	session, err := c.Authorize(ctx)
	if err != nil {
		return err
	}
	stations, err := c.Stations(ctx, session.Area)
	if err != nil {
		return err
	}
	if command == "stations" {
		fmt.Fprintln(errOut, "Area:", session.Area)
		for _, s := range stations {
			if s.TimeFree == "1" {
				fmt.Fprintf(out, "%s\t%s\n", s.ID, s.Name)
			}
		}
		return nil
	}
	available := false
	for _, s := range stations {
		if s.ID == station && s.TimeFree == "1" {
			available = true
		}
	}
	if !available {
		return fmt.Errorf("%s is not a time-free station in %s; use stations to list available stations", station, session.Area)
	}
	programs, err := c.Programs(ctx, session.Area, station, dateValue)
	if err != nil {
		return err
	}
	if command == "programs" {
		for _, p := range programs {
			note := ""
			if p.Unavailable {
				note = " [time-free unavailable]"
			}
			fmt.Fprintf(out, "%s\t%s\t%s%s\n", p.Start.Format(radiko.TimeLayout), p.End.Format(radiko.TimeLayout), p.Title, note)
		}
		return nil
	}
	selected, err := selectPrograms(programs, parsedStart, end, title, match, time.Now())
	if err != nil {
		return err
	}
	s, err := store.New(dataDir)
	if err != nil {
		return err
	}
	r, err := recorder.New()
	if err != nil {
		return err
	}
	r.Progress = func(message string) { fmt.Fprintln(errOut, message) }
	engine := &app.Engine{API: c, Store: s, Downloader: r, Timeout: timeout, Log: func(message string) { fmt.Fprintln(out, message) }}
	_, err = engine.Record(ctx, selected, false)
	return err
}

func episodeID(p radiko.Program) string { return app.EpisodeID(p) }

func parseDate(value string, now time.Time) (time.Time, error) {
	day := radiko.BroadcastDate(now)
	switch value {
	case "today":
		return day, nil
	case "yesterday":
		return day.AddDate(0, 0, -1), nil
	default:
		t, err := time.ParseInLocation("2006-01-02", value, radiko.JST)
		if err != nil {
			return time.Time{}, fmt.Errorf("date must be YYYY-MM-DD, today, or yesterday")
		}
		return t, nil
	}
}

func selectPrograms(programs []radiko.Program, start time.Time, end, title, match string, now time.Time) ([]radiko.Program, error) {
	var selected []radiko.Program
	for _, p := range programs {
		if !start.IsZero() {
			if start.Before(p.Start) || !start.Before(p.End) {
				continue
			}
			p.Start = start
			if end != "" {
				t, err := radiko.ParseTime(end)
				if err != nil {
					return nil, err
				}
				if !t.After(start) || t.After(p.End) {
					return nil, fmt.Errorf("end must be within the selected program and after start")
				}
				p.End = t
			}
			if title != "" {
				p.Title = title
			}
		} else {
			if !strings.Contains(strings.ToLower(p.Title), strings.ToLower(match)) {
				continue
			}
		}
		if p.Unavailable {
			return nil, fmt.Errorf("time-free unavailable: %s", p.Title)
		}
		if p.End.After(now.Add(-3 * time.Minute)) {
			if start.IsZero() {
				continue
			}
			return nil, fmt.Errorf("wait at least 3 minutes after the requested end time")
		}
		if p.Start.Before(now.Add(-7 * 24 * time.Hour)) {
			return nil, fmt.Errorf("this version supports only the past 7 days")
		}
		selected = append(selected, p)
	}
	if len(selected) == 0 {
		return nil, fmt.Errorf("no finished programs matched; check programs -station ID -date DATE")
	}
	return selected, nil
}

func serve(ctx context.Context, h http.Handler, listen, baseURL string, errOut io.Writer) error {
	listener, err := net.Listen("tcp", listen)
	if err != nil {
		return err
	}
	return serveListener(ctx, h, listener, baseURL, errOut)
}

func serveListener(ctx context.Context, h http.Handler, listener net.Listener, baseURL string, errOut io.Writer) error {
	srv := &http.Server{Handler: h, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second, ErrorLog: log.New(errOut, "http: ", log.LstdFlags)}
	finished := make(chan error, 1)
	go func() { finished <- srv.Serve(listener) }()
	fmt.Fprintf(errOut, "listening on %s; feed: %s/feed.xml\n", listener.Addr(), strings.TrimRight(baseURL, "/"))
	select {
	case err := <-finished:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			_ = srv.Close()
			return err
		}
		return nil
	}
}
