package main

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/kkato/radipod/internal/radiko"
)

func TestSelectionAndBroadcastDate(t *testing.T) {
	start, _ := radiko.ParseTime("20261016010000")
	program := radiko.Program{Station: "LFR", Title: "好きな番組", Start: start, End: start.Add(2 * time.Hour)}
	date, err := parseDate("today", start)
	if err != nil || date.Format("2006-01-02") != "2026-10-15" {
		t.Fatalf("%v %v", date, err)
	}
	ps, err := selectPrograms([]radiko.Program{program}, start, "20261016010010", "test clip", "", start.Add(24*time.Hour))
	if err != nil || len(ps) != 1 || ps[0].End.Sub(ps[0].Start) != 10*time.Second {
		t.Fatalf("%v %v", ps, err)
	}
	if episodeID(ps[0]) == episodeID(program) {
		t.Fatal("clip and full program collide")
	}
	if _, err := selectPrograms([]radiko.Program{program}, start, "20261016040000", "", "", start.Add(24*time.Hour)); err == nil {
		t.Fatal("allowed range across programs")
	}
	if _, err := selectPrograms([]radiko.Program{program}, start, "", "", "", program.End.Add(time.Minute)); err == nil {
		t.Fatal("allowed recording before availability delay")
	}
	if _, err := selectPrograms([]radiko.Program{program}, start, "", "", "", start.Add(8*24*time.Hour)); err == nil {
		t.Fatal("allowed expired program")
	}
	ps, err = selectPrograms([]radiko.Program{program}, time.Time{}, "", "", "好き", start.Add(24*time.Hour))
	if err != nil || len(ps) != 1 {
		t.Fatalf("%v %v", ps, err)
	}
}

func TestInvalidCLIRejectedBeforeNetwork(t *testing.T) {
	for _, args := range [][]string{
		{"record"}, {"record", "-station", "../bad", "-start", "20261016010000"},
		{"record", "-station", "LFR", "-start", "invalid"},
		{"record", "-station", "LFR", "-date", "yesterday"},
		{"record", "-station", "LFR", "-start", "20261016010000", "-match", "test"},
		{"record", "-station", "LFR", "-start", "20261016010000", "-end", "20261015010000"},
		{"serve", "-base-url", "http://example.com/prefix"},
		{"serve", "-base-url", "http://user:secret@example.com"},
	} {
		var out, errOut bytes.Buffer
		if err := run(context.Background(), args, &out, &errOut); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
}
