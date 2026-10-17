package radiko

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestAuthorizeAndProgramDiscovery(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v2/api/auth1":
			if r.Header.Get("X-Radiko-App") != "pc_html5" {
				t.Error("missing app header")
			}
			w.Header().Set("X-Radiko-AuthToken", "test-token")
			w.Header().Set("X-Radiko-KeyOffset", "2")
			w.Header().Set("X-Radiko-KeyLength", "8")
		case "/v2/api/auth2":
			if r.Header.Get("X-Radiko-PartialKey") != base64.StdEncoding.EncodeToString([]byte(authKey[2:10])) || r.Header.Get("X-Radiko-AuthToken") != "test-token" {
				t.Error("wrong challenge response")
			}
			fmt.Fprint(w, "JP13,東京都,tokyo\n")
		case "/v3/station/list/JP13.xml":
			fmt.Fprint(w, `<stations><station><id>LFR</id><name>ニッポン放送</name><timefree>1</timefree></station></stations>`)
		case "/program/v3/date/20261015/area/JP13.xml":
			fmt.Fprint(w, `<radiko><stations><station id="LFR"><progs><prog ft="20261016010000" to="20261016030000"><title>A &amp; B</title><desc>説明</desc><ts_in_ng>0</ts_in_ng></prog></progs></station></stations></radiko>`)
		case "/v3/station/stream/pc_html5/LFR.xml":
			fmt.Fprint(w, `<urls><url timefree="0" areafree="0"><playlist_create_url>https://example.com/live</playlist_create_url></url><url timefree="1" areafree="1"><playlist_create_url>https://example.com/premium</playlist_create_url></url><url timefree="1" areafree="0"><playlist_create_url>https://example.com/tf</playlist_create_url></url></urls>`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c := &Client{HTTP: srv.Client(), APIURL: srv.URL, SiteURL: srv.URL}
	ctx := context.Background()
	s, err := c.Authorize(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if s.Area != "JP13" {
		t.Fatal(s)
	}
	stations, err := c.Stations(ctx, s.Area)
	if err != nil || len(stations) != 1 {
		t.Fatalf("%v %v", stations, err)
	}
	start, _ := ParseTime("20261016010000")
	ps, err := c.Programs(ctx, s.Area, "LFR", BroadcastDate(start))
	if err != nil {
		t.Fatal(err)
	}
	if len(ps) != 1 || ps[0].Title != "A & B" || !ps[0].Start.Equal(start) {
		t.Fatal(ps)
	}
	urls, err := c.Playlists(ctx, "LFR")
	if err != nil || len(urls) != 1 || urls[0] != "https://example.com/tf" {
		t.Fatalf("%v %v", urls, err)
	}
}

func TestBadAuthChallengeDoesNotPanic(t *testing.T) {
	for _, pair := range [][2]string{{"-1", "3"}, {"0", "0"}, {"99", "3"}, {"3", "999999999999999999999"}, {"x", "2"}} {
		t.Run(strings.Join(pair[:], "/"), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("X-Radiko-AuthToken", "test")
				w.Header().Set("X-Radiko-KeyOffset", pair[0])
				w.Header().Set("X-Radiko-KeyLength", pair[1])
			}))
			defer srv.Close()
			c := &Client{HTTP: srv.Client(), APIURL: srv.URL}
			if _, err := c.Authorize(context.Background()); err == nil {
				t.Fatal("accepted invalid challenge")
			}
		})
	}
}

func TestChunkURLPreservesStartAndAdvancesSeek(t *testing.T) {
	start, _ := ParseTime("20261016010000")
	raw, err := ChunkURL("https://example.com/tf?existing=1", "LFR", start, start.Add(5*time.Minute), 300, "id")
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(raw)
	q := u.Query()
	for key, want := range map[string]string{"ft": "20261016010000", "seek": "20261016010500", "to": "20261016011000", "l": "300", "existing": "1", "station_id": "LFR"} {
		if q.Get(key) != want {
			t.Errorf("%s=%s, want %s", key, q.Get(key), want)
		}
	}
}

func TestHTTPFailureAndOutOfArea(t *testing.T) {
	for _, status := range []int{http.StatusForbidden, http.StatusOK} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Radiko-AuthToken", "test")
			w.Header().Set("X-Radiko-KeyOffset", "0")
			w.Header().Set("X-Radiko-KeyLength", "4")
			w.WriteHeader(status)
			fmt.Fprint(w, "OUT")
		}))
		c := &Client{HTTP: srv.Client(), APIURL: srv.URL}
		if _, err := c.Authorize(context.Background()); err == nil {
			t.Error("expected error")
		}
		srv.Close()
	}
}
