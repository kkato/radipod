package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"time"

	"github.com/kkato/radipod/internal/app"
	"github.com/kkato/radipod/internal/config"
	"github.com/kkato/radipod/internal/radiko"
	"github.com/kkato/radipod/internal/recorder"
	"github.com/kkato/radipod/internal/server"
	"github.com/kkato/radipod/internal/store"
)

func runConfigured(ctx context.Context, command string, args []string, out, errOut io.Writer) error {
	f := flag.NewFlagSet(command, flag.ContinueOnError)
	f.SetOutput(errOut)
	path := f.String("config", "config.local.json", "JSON configuration file")
	dryRun := false
	if command == "sync" {
		f.BoolVar(&dryRun, "dry-run", false, "list missing matches without recording audio")
	}
	if err := f.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if f.NArg() != 0 {
		return fmt.Errorf("unexpected positional arguments")
	}
	cfg, err := config.Load(*path)
	if err != nil {
		return err
	}
	s, err := store.New(cfg.DataDir)
	if err != nil {
		return err
	}
	logger := log.New(errOut, "radipod: ", log.LstdFlags)
	timeout, _ := time.ParseDuration(cfg.RecordTimeout) // validated by config.Load
	engine := &app.Engine{API: radiko.NewClient(), Store: s, Timeout: timeout, Log: func(message string) { logger.Print(message) }}
	if !dryRun && len(cfg.Rules) > 0 {
		r, err := recorder.New()
		if err != nil {
			return err
		}
		r.Progress = engine.Log
		engine.Downloader = r
	}
	if command == "sync" {
		engine.Log = func(message string) { fmt.Fprintln(out, message) }
		_, err := engine.Sync(ctx, cfg, time.Now(), dryRun)
		return err
	}
	h, err := server.New(s, server.Config{BaseURL: cfg.BaseURL, Title: cfg.Title, ImageURL: cfg.ImageURL, User: os.Getenv("RADIPOD_HTTP_USER"), Password: os.Getenv("RADIPOD_HTTP_PASSWORD")})
	if err != nil {
		return err
	}
	// Bind first: a port conflict must not start a background recording.
	listener, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		return err
	}
	workerCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	interval, _ := time.ParseDuration(cfg.PollInterval)
	go func() {
		defer close(done)
		app.Watch(workerCtx, interval, func(ctx context.Context) error {
			_, err := engine.Sync(ctx, cfg, time.Now(), false)
			return err
		}, func(err error) { logger.Printf("sync failed; retry next pass: %v", err) })
	}()
	err = serveListener(workerCtx, h, listener, cfg.BaseURL, errOut)
	cancel()
	<-done
	return err
}
