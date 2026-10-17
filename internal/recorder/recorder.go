package recorder

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/kkato/radipod/internal/radiko"
)

type Runner func(context.Context, string, string, ...string) ([]byte, error)

type Recorder struct {
	FFmpeg   string
	FFprobe  string
	Run      Runner
	Progress func(string)
}

func New() (*Recorder, error) {
	f, err := exec.LookPath("ffmpeg")
	if err != nil {
		return nil, fmt.Errorf("ffmpeg is required: %w", err)
	}
	p, err := exec.LookPath("ffprobe")
	if err != nil {
		return nil, fmt.Errorf("ffprobe is required: %w", err)
	}
	return &Recorder{FFmpeg: f, FFprobe: p, Run: run}, nil
}

// limitBuffer bounds diagnostic output from external commands.
type limitBuffer struct{ bytes.Buffer }

func (b *limitBuffer) Write(p []byte) (int, error) {
	n := len(p)
	if remaining := (16 << 10) - b.Len(); remaining > 0 {
		if len(p) > remaining {
			p = p[:remaining]
		}
		_, _ = b.Buffer.Write(p)
	}
	return n, nil
}

func run(ctx context.Context, name, dir string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.WaitDelay = 5 * time.Second
	var stdout, stderr limitBuffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("%s: %w: %s", filepath.Base(name), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

func (r *Recorder) probe(ctx context.Context, path string) (float64, error) {
	out, err := r.Run(ctx, r.FFprobe, "", "-v", "error", "-select_streams", "a:0", "-show_entries", "stream=codec_type:format=duration", "-of", "json", path)
	if err != nil {
		return 0, err
	}
	var info struct {
		Streams []struct {
			Type string `json:"codec_type"`
		} `json:"streams"`
		Format struct {
			Duration string `json:"duration"`
		} `json:"format"`
	}
	if err := json.Unmarshal(out, &info); err != nil {
		return 0, err
	}
	d, err := strconv.ParseFloat(info.Format.Duration, 64)
	if err != nil || math.IsNaN(d) || math.IsInf(d, 0) || d <= 0 || len(info.Streams) == 0 || info.Streams[0].Type != "audio" {
		return 0, fmt.Errorf("ffprobe found no valid audio duration")
	}
	return d, nil
}

// Download fetches <= 5-minute chunks, retries each chunk, then remuxes to M4A.
// It never publishes files itself; store.Save controls when an episode is visible.
func (r *Recorder) Download(ctx context.Context, session radiko.Session, p radiko.Program, playlists []string, output string) (duration float64, retErr error) {
	defer func() {
		if retErr != nil && session.Token != "" {
			retErr = fmt.Errorf("%s", strings.ReplaceAll(retErr.Error(), session.Token, "[redacted]"))
		}
	}()
	if !p.End.After(p.Start) || p.End.Sub(p.Start) > 24*time.Hour || len(playlists) == 0 {
		return 0, fmt.Errorf("invalid recording range or empty playlists")
	}
	if strings.ContainsAny(session.Token+session.Area, "\r\n") {
		return 0, fmt.Errorf("invalid session headers")
	}
	work, err := os.MkdirTemp(filepath.Dir(output), ".chunks-")
	if err != nil {
		return 0, err
	}
	defer os.RemoveAll(work)
	id, err := radiko.NewStreamID()
	if err != nil {
		return 0, err
	}
	headers := "X-Radiko-AuthToken: " + session.Token + "\r\nX-Radiko-AreaId: " + session.Area + "\r\n"
	seek := p.Start
	var files []string
	for index := 0; seek.Before(p.End); index++ {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		seconds := min(300, int(math.Ceil(p.End.Sub(seek).Seconds()/5))*5)
		name := fmt.Sprintf("chunk-%05d.m4a", index)
		path := filepath.Join(work, name)
		var chunkDuration float64
		var lastErr error
		success := false
		for attempt := 0; attempt < 2 && !success; attempt++ {
			if attempt > 0 {
				select {
				case <-ctx.Done():
					return 0, ctx.Err()
				case <-time.After(2 * time.Second):
				}
			}
			for _, playlist := range playlists {
				address, err := radiko.ChunkURL(playlist, p.Station, p.Start, seek, seconds, id)
				if err != nil {
					return 0, err
				}
				chunkCtx, cancel := context.WithTimeout(ctx, 3*time.Minute)
				_, lastErr = r.Run(chunkCtx, r.FFmpeg, work,
					"-nostdin", "-hide_banner", "-loglevel", "error", "-fflags", "+discardcorrupt",
					"-rw_timeout", "30000000", "-headers", headers, "-http_seekable", "0", "-seekable", "0",
					"-i", address, "-map", "0:a:0", "-vn", "-c:a", "copy", "-bsf:a", "aac_adtstoasc", "-y", path)
				if lastErr == nil {
					chunkDuration, lastErr = r.probe(chunkCtx, path)
				}
				cancel()
				if lastErr == nil && math.Abs(chunkDuration-float64(seconds)) > 2.5 {
					lastErr = fmt.Errorf("incomplete chunk: expected %ds, got %.3fs", seconds, chunkDuration)
				}
				if lastErr == nil {
					success = true
					break
				}
				if ctx.Err() != nil {
					return 0, ctx.Err()
				}
			}
		}
		if !success {
			return 0, fmt.Errorf("chunk at %s: %w", seek.Format(time.RFC3339), lastErr)
		}
		files = append(files, "file '"+name+"'")
		step := int(math.Round(chunkDuration))
		if step < 1 {
			return 0, fmt.Errorf("chunk made no progress")
		}
		seek = seek.Add(time.Duration(step) * time.Second)
		if r.Progress != nil {
			r.Progress(fmt.Sprintf("%s: %.0f / %.0f seconds", p.Station, min(seek.Sub(p.Start).Seconds(), p.End.Sub(p.Start).Seconds()), p.End.Sub(p.Start).Seconds()))
		}
	}
	list := filepath.Join(work, "concat.txt")
	if err := os.WriteFile(list, []byte(strings.Join(files, "\n")+"\n"), 0600); err != nil {
		return 0, err
	}
	_, err = r.Run(ctx, r.FFmpeg, work, "-nostdin", "-hide_banner", "-loglevel", "error", "-f", "concat", "-safe", "1", "-i", list,
		"-map", "0:a:0", "-c:a", "copy", "-t", strconv.FormatFloat(p.End.Sub(p.Start).Seconds(), 'f', 0, 64),
		"-metadata", "title="+p.Title, "-movflags", "+faststart", "-y", output)
	if err != nil {
		return 0, err
	}
	duration, err = r.probe(ctx, output)
	if err != nil {
		return 0, err
	}
	if math.Abs(duration-p.End.Sub(p.Start).Seconds()) > 2.5 {
		return 0, errors.New("final audio duration does not match requested range")
	}
	return duration, nil
}
