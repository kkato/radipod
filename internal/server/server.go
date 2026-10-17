package server

import (
	"crypto/sha256"
	"crypto/subtle"
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/kkato/radipod/internal/feed"
	"github.com/kkato/radipod/internal/store"
)

type Config struct{ BaseURL, Title, User, Password string }

func New(s *store.Store, cfg Config) (http.Handler, error) {
	base, err := feed.ValidateBaseURL(cfg.BaseURL)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(cfg.Title) == "" {
		return nil, fmt.Errorf("feed title is required")
	}
	if (cfg.User == "") != (cfg.Password == "") {
		return nil, fmt.Errorf("set both RADIPOD_HTTP_USER and RADIPOD_HTTP_PASSWORD")
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		fmt.Fprintln(w, cfg.Title+"\n"+base+"/feed.xml")
	})
	mux.HandleFunc("GET /feed.xml", func(w http.ResponseWriter, r *http.Request) {
		episodes, err := s.List()
		if err != nil {
			log.Printf("read episodes: %v", err)
			http.Error(w, "cannot read episodes", 500)
			return
		}
		data, err := feed.Generate(episodes, base, cfg.Title)
		if err != nil {
			log.Printf("generate feed: %v", err)
			http.Error(w, "cannot generate feed", 500)
			return
		}
		w.Header().Set("Content-Type", "application/rss+xml; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		_, _ = w.Write(data)
	})
	mux.HandleFunc("GET /media/{file}", func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("file")
		if !strings.HasSuffix(name, ".m4a") {
			http.NotFound(w, r)
			return
		}
		id := strings.TrimSuffix(name, ".m4a")
		if !store.ValidID(id) {
			http.NotFound(w, r)
			return
		}
		f, err := s.OpenAudio(id)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		defer f.Close()
		info, err := f.Stat()
		if err != nil {
			http.Error(w, "cannot read audio", 500)
			return
		}
		w.Header().Set("Content-Type", "audio/mp4")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		http.ServeContent(w, r, name, info.ModTime(), f)
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if cfg.User != "" {
			user, pass, ok := r.BasicAuth()
			u, p := sha256.Sum256([]byte(user)), sha256.Sum256([]byte(pass))
			wantU, wantP := sha256.Sum256([]byte(cfg.User)), sha256.Sum256([]byte(cfg.Password))
			valid := subtle.ConstantTimeCompare(u[:], wantU[:]) & subtle.ConstantTimeCompare(p[:], wantP[:])
			if !ok || valid != 1 {
				w.Header().Set("WWW-Authenticate", `Basic realm="radipod", charset="UTF-8"`)
				http.Error(w, "authentication required", 401)
				return
			}
		}
		mux.ServeHTTP(w, r)
	}), nil
}
