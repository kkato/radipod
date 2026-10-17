package recorder

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kkato/radipod/internal/radiko"
)

func TestChunkingFallbackAndConcatenation(t *testing.T) {
	start, _ := radiko.ParseTime("20261016010000")
	p := radiko.Program{Station: "LFR", Title: "test", Start: start, End: start.Add(610 * time.Second)}
	var seeks []string
	duration := "300"
	r := &Recorder{FFmpeg: "ffmpeg", FFprobe: "ffprobe"}
	r.Run = func(ctx context.Context, name, dir string, args ...string) ([]byte, error) {
		if name == "ffprobe" {
			return []byte(fmt.Sprintf(`{"streams":[{"codec_type":"audio"}],"format":{"duration":"%s"}}`, duration)), nil
		}
		for _, arg := range args {
			if strings.HasPrefix(arg, "https://broken.example") {
				return nil, fmt.Errorf("temporary failure")
			}
			if strings.HasPrefix(arg, "https://working.example") {
				u, _ := url.Parse(arg)
				q := u.Query()
				if q.Get("ft") != "20261016010000" {
					t.Fatal("start shifted")
				}
				seeks = append(seeks, q.Get("seek"))
				duration = q.Get("l")
				return nil, os.WriteFile(args[len(args)-1], []byte("chunk"), 0600)
			}
		}
		list, err := os.ReadFile(filepath.Join(dir, "concat.txt"))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Count(string(list), "file '") != 3 {
			t.Fatal(string(list))
		}
		duration = "610"
		return nil, os.WriteFile(args[len(args)-1], []byte("complete"), 0600)
	}
	d, err := r.Download(context.Background(), radiko.Session{Token: "secret", Area: "JP13"}, p, []string{"https://broken.example/tf", "https://working.example/tf"}, filepath.Join(t.TempDir(), "audio.m4a"))
	if err != nil || d != 610 {
		t.Fatalf("%f %v", d, err)
	}
	if strings.Join(seeks, ",") != "20261016010000,20261016010500,20261016011000" {
		t.Fatal(seeks)
	}
}

func TestRejectsTruncatedAudioAndRedactsToken(t *testing.T) {
	start, _ := radiko.ParseTime("20261016010000")
	p := radiko.Program{Station: "LFR", Start: start, End: start.Add(300 * time.Second)}
	r := &Recorder{FFmpeg: "ffmpeg", FFprobe: "ffprobe", Run: func(_ context.Context, name, dir string, args ...string) ([]byte, error) {
		if name == "ffprobe" {
			return []byte(`{"streams":[{"codec_type":"audio"}],"format":{"duration":"10"}}`), nil
		}
		return nil, nil
	}}
	_, err := r.Download(context.Background(), radiko.Session{Token: "private-token", Area: "JP13"}, p, []string{"https://example.com/tf"}, filepath.Join(t.TempDir(), "audio.m4a"))
	if err == nil || !strings.Contains(err.Error(), "incomplete chunk") {
		t.Fatalf("accepted truncated recording: %v", err)
	}
	r.Run = func(context.Context, string, string, ...string) ([]byte, error) {
		return nil, fmt.Errorf("request failed with private-token")
	}
	_, err = r.Download(context.Background(), radiko.Session{Token: "private-token", Area: "JP13"}, p, []string{"https://example.com/tf"}, filepath.Join(t.TempDir(), "audio.m4a"))
	if err == nil || strings.Contains(err.Error(), "private-token") {
		t.Fatalf("token leaked: %v", err)
	}
}

// This exercises the actual FFmpeg/ffprobe path with synthetic audio, not radiko.
func TestFFmpegLocalHLS(t *testing.T) {
	r, err := New()
	if err != nil {
		t.Skip(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	dir := t.TempDir()
	_, err = run(ctx, r.FFmpeg, dir, "-nostdin", "-loglevel", "error", "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000", "-t", "10", "-c:a", "aac", "-f", "hls", "-hls_time", "5", "-hls_list_size", "0", filepath.Join(dir, "test.m3u8"))
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.FileServer(http.Dir(dir)))
	defer srv.Close()
	start := time.Now().Truncate(time.Second)
	p := radiko.Program{Station: "TEST", Title: "synthetic tone", Start: start, End: start.Add(10 * time.Second)}
	d, err := r.Download(ctx, radiko.Session{Token: "test", Area: "JP13"}, p, []string{srv.URL + "/test.m3u8"}, filepath.Join(t.TempDir(), "audio.m4a"))
	if err != nil {
		t.Fatal(err)
	}
	if d < 9.8 || d > 10.2 {
		t.Fatal(d)
	}
}
