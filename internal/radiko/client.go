// Package radiko implements the unofficial, in-area HTML5 time-free protocol.
// Protocol reference: uru2/rec_radiko_ts (MIT); see THIRD_PARTY_NOTICES.md.
package radiko

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const TimeLayout = "20060102150405"
const authKey = "bcd151073c03b352e1ef2fd66c32209da9ca0afa"

var JST = time.FixedZone("JST", 9*60*60)
var stationPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,40}$`)
var areaPattern = regexp.MustCompile(`^JP([1-9]|[1-3][0-9]|4[0-7])$`)

type Client struct {
	HTTP    *http.Client
	APIURL  string
	SiteURL string
}

type Session struct{ Token, Area string }

type Station struct {
	ID       string `xml:"id"`
	Name     string `xml:"name"`
	TimeFree string `xml:"timefree"`
}

type Program struct {
	Station     string
	Title       string
	Description string
	Start       time.Time
	End         time.Time
	Unavailable bool
}

func NewClient() *Client {
	return &Client{HTTP: &http.Client{Timeout: 30 * time.Second}, APIURL: "https://api.radiko.jp", SiteURL: "https://radiko.jp"}
}

func ValidateStation(id string) error {
	if !stationPattern.MatchString(id) {
		return fmt.Errorf("invalid station ID %q", id)
	}
	return nil
}

func ParseTime(value string) (time.Time, error) {
	if len(value) != 14 {
		return time.Time{}, fmt.Errorf("use YYYYMMDDhhmmss (JST): %q", value)
	}
	t, err := time.ParseInLocation(TimeLayout, value, JST)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid JST datetime %q: %w", value, err)
	}
	return t, nil
}

// BroadcastDate accounts for radiko's 05:00 boundary.
func BroadcastDate(t time.Time) time.Time { return t.In(JST).Add(-5 * time.Hour) }

func (c *Client) get(ctx context.Context, address string, headers http.Header) ([]byte, http.Header, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, nil, err
	}
	req.Header = headers.Clone()
	if req.Header == nil {
		req.Header = make(http.Header)
	}
	req.Header.Set("User-Agent", "radipod/0.1")
	res, err := c.HTTP.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("radiko request: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, nil, fmt.Errorf("radiko %s: HTTP %d", req.URL.Path, res.StatusCode)
	}
	const limit = 16 << 20
	body, err := io.ReadAll(io.LimitReader(res.Body, limit+1))
	if err != nil {
		return nil, nil, err
	}
	if len(body) > limit {
		return nil, nil, fmt.Errorf("radiko response exceeds %d bytes", limit)
	}
	return body, res.Header, nil
}

func (c *Client) Authorize(ctx context.Context) (Session, error) {
	headers := http.Header{}
	headers.Set("X-Radiko-App", "pc_html5")
	headers.Set("X-Radiko-App-Version", "0.0.1")
	headers.Set("X-Radiko-Device", "pc")
	headers.Set("X-Radiko-User", "dummy_user")
	_, h, err := c.get(ctx, c.APIURL+"/v2/api/auth1", headers)
	if err != nil {
		return Session{}, fmt.Errorf("auth1: %w", err)
	}
	token := h.Get("X-Radiko-AuthToken")
	offset, err1 := strconv.Atoi(h.Get("X-Radiko-KeyOffset"))
	length, err2 := strconv.Atoi(h.Get("X-Radiko-KeyLength"))
	if token == "" || strings.ContainsAny(token, "\r\n") || err1 != nil || err2 != nil || offset < 0 || length <= 0 || offset > len(authKey) || length > len(authKey)-offset {
		return Session{}, fmt.Errorf("auth1 returned invalid challenge headers")
	}
	headers.Set("X-Radiko-AuthToken", token)
	headers.Set("X-Radiko-PartialKey", base64.StdEncoding.EncodeToString([]byte(authKey[offset:offset+length])))
	body, _, err := c.get(ctx, c.APIURL+"/v2/api/auth2", headers)
	if err != nil {
		return Session{}, fmt.Errorf("auth2: %w", err)
	}
	area := strings.TrimSpace(strings.SplitN(string(body), ",", 2)[0])
	if !areaPattern.MatchString(area) {
		return Session{}, fmt.Errorf("radiko could not authorize this connection's area")
	}
	return Session{Token: token, Area: area}, nil
}

func (c *Client) Stations(ctx context.Context, area string) ([]Station, error) {
	if !areaPattern.MatchString(area) {
		return nil, fmt.Errorf("invalid area ID")
	}
	body, _, err := c.get(ctx, c.SiteURL+"/v3/station/list/"+area+".xml", nil)
	if err != nil {
		return nil, err
	}
	var result struct {
		Stations []Station `xml:"station"`
	}
	if err := xml.Unmarshal(body, &result); err != nil {
		return nil, err
	}
	if len(result.Stations) == 0 {
		return nil, fmt.Errorf("station list is empty")
	}
	return result.Stations, nil
}

func (c *Client) Programs(ctx context.Context, area, station string, date time.Time) ([]Program, error) {
	if err := ValidateStation(station); err != nil {
		return nil, err
	}
	if !areaPattern.MatchString(area) {
		return nil, fmt.Errorf("invalid area ID")
	}
	address := c.APIURL + "/program/v3/date/" + date.In(JST).Format("20060102") + "/area/" + area + ".xml"
	body, _, err := c.get(ctx, address, nil)
	if err != nil {
		return nil, err
	}
	var result struct {
		Stations []struct {
			ID       string `xml:"id,attr"`
			Programs []struct {
				Start       string `xml:"ft,attr"`
				End         string `xml:"to,attr"`
				Title       string `xml:"title"`
				Description string `xml:"desc"`
				Info        string `xml:"info"`
				Blocked     string `xml:"ts_in_ng"`
			} `xml:"progs>prog"`
		} `xml:"stations>station"`
	}
	if err := xml.Unmarshal(body, &result); err != nil {
		return nil, err
	}
	var programs []Program
	for _, s := range result.Stations {
		if s.ID != station {
			continue
		}
		for _, p := range s.Programs {
			start, err := ParseTime(p.Start)
			if err != nil {
				return nil, err
			}
			end, err := ParseTime(p.End)
			if err != nil {
				return nil, err
			}
			if !end.After(start) {
				return nil, fmt.Errorf("invalid program duration")
			}
			desc := strings.TrimSpace(p.Description + "\n" + p.Info)
			programs = append(programs, Program{station, p.Title, desc, start, end, p.Blocked == "1"})
		}
	}
	if len(programs) == 0 {
		return nil, fmt.Errorf("no programs for %s on %s in %s", station, date.Format("2006-01-02"), area)
	}
	return programs, nil
}

func (c *Client) Playlists(ctx context.Context, station string) ([]string, error) {
	if err := ValidateStation(station); err != nil {
		return nil, err
	}
	body, _, err := c.get(ctx, c.APIURL+"/v3/station/stream/pc_html5/"+station+".xml", nil)
	if err != nil {
		return nil, err
	}
	var result struct {
		URLs []struct {
			AreaFree string `xml:"areafree,attr"`
			TimeFree string `xml:"timefree,attr"`
			Playlist string `xml:"playlist_create_url"`
		} `xml:"url"`
	}
	if err := xml.Unmarshal(body, &result); err != nil {
		return nil, err
	}
	var urls []string
	for _, item := range result.URLs {
		if item.AreaFree != "0" || item.TimeFree != "1" {
			continue
		}
		p := strings.TrimSpace(item.Playlist)
		u, err := url.Parse(p)
		if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
			return nil, fmt.Errorf("invalid playlist URL")
		}
		urls = append(urls, p)
	}
	if len(urls) == 0 {
		return nil, fmt.Errorf("no in-area time-free stream for %s", station)
	}
	return urls, nil
}

func NewStreamID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func ChunkURL(base, station string, start, seek time.Time, seconds int, streamID string) (string, error) {
	u, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	q := u.Query()
	q.Set("station_id", station)
	q.Set("start_at", start.In(JST).Format(TimeLayout))
	q.Set("ft", start.In(JST).Format(TimeLayout))
	q.Set("seek", seek.In(JST).Format(TimeLayout))
	end := seek.Add(time.Duration(seconds) * time.Second).In(JST).Format(TimeLayout)
	q.Set("end_at", end)
	q.Set("to", end)
	q.Set("l", strconv.Itoa(seconds))
	q.Set("lsid", streamID)
	q.Set("type", "c")
	u.RawQuery = q.Encode()
	return u.String(), nil
}
