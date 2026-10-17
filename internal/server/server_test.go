package server

import (
	"encoding/xml"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/kkato/radipod/internal/feed"
	"github.com/kkato/radipod/internal/store"
)

func TestFeedAndAudioDownload(t *testing.T) {
	s, _ := store.New(t.TempDir())
	start := time.Date(2026, 10, 16, 1, 0, 0, 0, time.FixedZone("JST", 9*3600))
	e := store.Episode{ID: "LFR_20261016010000", Station: "LFR", Title: "ラジオ & <音楽>", Description: "説明 <br> テスト", Start: start, End: start.Add(time.Minute)}
	_, err := s.Save(e, func(path string) (float64, error) { return 60, os.WriteFile(path, []byte("0123456789"), 0600) })
	if err != nil {
		t.Fatal(err)
	}
	h, err := New(s, Config{BaseURL: "http://radio.example:8080", Title: "radipod"})
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/feed.xml", nil))
	if w.Code != 200 || !strings.HasPrefix(w.Header().Get("Content-Type"), "application/rss+xml") {
		t.Fatal(w)
	}
	var rss feed.RSS
	if err := xml.Unmarshal(w.Body.Bytes(), &rss); err != nil {
		t.Fatal(err)
	}
	if len(rss.Channel.Items) != 1 {
		t.Fatal(rss)
	}
	item := rss.Channel.Items[0]
	if item.Title != e.Title || item.GUID.IsPermaLink != "false" || item.GUID.Value != "radipod:"+e.ID || item.Enclosure.Length != "10" || item.Enclosure.Type != "audio/mp4" || item.Enclosure.URL != "http://radio.example:8080/media/"+e.ID+".m4a" {
		t.Fatal(item)
	}
	if _, err := time.Parse(time.RFC1123Z, item.PubDate); err != nil {
		t.Fatal(err)
	}
	for _, method := range []string{"GET", "HEAD"} {
		r := httptest.NewRequest(method, "/media/"+e.ID+".m4a", nil)
		r.Header.Set("Range", "bytes=2-5")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusPartialContent || w.Header().Get("Content-Range") != "bytes 2-5/10" {
			t.Fatal(w)
		}
		if method == "GET" && w.Body.String() != "2345" {
			t.Fatal(w.Body.String())
		}
		if method == "HEAD" && w.Body.Len() != 0 {
			t.Fatal("HEAD returned a body")
		}
	}
	for _, path := range []string{"/media/episode.json", "/media/.pending.m4a", "/media/missing.m4a", "/episodes/" + e.ID + "/episode.json"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 404 {
			t.Fatalf("%s: %d", path, w.Code)
		}
	}
	// Changing the serving address must not change episode identity.
	es, _ := s.List()
	changed, err := feed.Generate(es, "https://different.example", "test")
	if err != nil {
		t.Fatal(err)
	}
	var rss2 feed.RSS
	_ = xml.Unmarshal(changed, &rss2)
	if rss2.Channel.Items[0].GUID != item.GUID {
		t.Fatal("GUID changed with host")
	}
}

func TestAuthenticationCoversFeedAndMedia(t *testing.T) {
	s, _ := store.New(t.TempDir())
	h, err := New(s, Config{BaseURL: "http://example.com", Title: "test", User: "user", Password: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/feed.xml", "/media/test.m4a", "/"} {
		r := httptest.NewRequest("GET", path, nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 401 {
			t.Fatalf("%s accessible without credentials", path)
		}
	}
	r := httptest.NewRequest("GET", "/feed.xml", nil)
	r.SetBasicAuth("user", "secret")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatal(w)
	}
	if _, err := New(s, Config{BaseURL: "http://example.com", Title: "test", User: "user"}); err == nil {
		t.Fatal("accepted partial credentials")
	}
}

func TestNoPendingOrCorruptAudioPublished(t *testing.T) {
	s, _ := store.New(t.TempDir())
	h, _ := New(s, Config{BaseURL: "http://example.com", Title: "test"})
	_ = os.Mkdir(s.Dir+"/.pending-test", 0700)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/feed.xml", nil))
	if w.Code != 200 {
		t.Fatal(w)
	}
	if strings.Contains(w.Body.String(), "<item>") {
		t.Fatal("pending file published")
	}
	_ = os.Mkdir(s.Dir+"/corrupt", 0700)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/feed.xml", nil))
	if w.Code != 500 {
		t.Fatal("corrupt store was silently ignored")
	}
	b, _ := io.ReadAll(w.Result().Body)
	if strings.Contains(string(b), s.Dir) {
		t.Fatal("filesystem path leaked")
	}
}
