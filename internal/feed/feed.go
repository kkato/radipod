package feed

import (
	"encoding/xml"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/kkato/radipod/internal/store"
)

type RSS struct {
	XMLName         xml.Name `xml:"rss"`
	Version         string   `xml:"version,attr"`
	ITunesNamespace string   `xml:"xmlns:itunes,attr,omitempty"`
	Channel         Channel  `xml:"channel"`
}
type Channel struct {
	Title       string       `xml:"title"`
	Link        string       `xml:"link"`
	Description string       `xml:"description"`
	Language    string       `xml:"language"`
	Image       *Image       `xml:"image,omitempty"`
	ITunesImage *ITunesImage `xml:"itunes:image,omitempty"`
	Items       []Item       `xml:"item"`
}
type Image struct {
	URL   string `xml:"url"`
	Title string `xml:"title"`
	Link  string `xml:"link"`
}
type ITunesImage struct {
	Href string `xml:"href,attr"`
}

const ITunesNamespace = "http://www.itunes.com/dtds/podcast-1.0.dtd"

type Item struct {
	Title       string    `xml:"title"`
	Description string    `xml:"description"`
	PubDate     string    `xml:"pubDate"`
	GUID        GUID      `xml:"guid"`
	Enclosure   Enclosure `xml:"enclosure"`
}
type GUID struct {
	IsPermaLink string `xml:"isPermaLink,attr"`
	Value       string `xml:",chardata"`
}
type Enclosure struct {
	URL    string `xml:"url,attr"`
	Length string `xml:"length,attr"`
	Type   string `xml:"type,attr"`
}

func ValidateBaseURL(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u == nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return "", fmt.Errorf("base-url must be an absolute http(s) origin without credentials, path, query, or fragment")
	}
	return strings.TrimRight(u.String(), "/"), nil
}

func ValidateImageURL(raw string) error {
	if raw == "" {
		return nil
	}
	u, err := url.Parse(raw)
	if err != nil || u == nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.Fragment != "" {
		return fmt.Errorf("image_url must be an absolute http(s) URL without credentials or fragment")
	}
	return nil
}

func Generate(episodes []store.Episode, base, title, imageURL string) ([]byte, error) {
	base, err := ValidateBaseURL(base)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(title) == "" {
		return nil, fmt.Errorf("feed title is required")
	}
	if err := ValidateImageURL(imageURL); err != nil {
		return nil, err
	}
	rss := RSS{Version: "2.0", Channel: Channel{Title: title, Link: base + "/", Description: "Personal radio recordings", Language: "ja"}}
	if imageURL != "" {
		rss.ITunesNamespace = ITunesNamespace
		rss.Channel.Image = &Image{URL: imageURL, Title: title, Link: base + "/"}
		rss.Channel.ITunesImage = &ITunesImage{Href: imageURL}
	}
	for _, e := range episodes {
		rss.Channel.Items = append(rss.Channel.Items, Item{
			Title: e.Title, Description: e.Description, PubDate: e.Start.Format(time.RFC1123Z),
			GUID:      GUID{IsPermaLink: "false", Value: "radipod:" + e.ID},
			Enclosure: Enclosure{URL: base + "/media/" + url.PathEscape(e.ID) + ".m4a", Length: strconv.FormatInt(e.Size, 10), Type: "audio/mp4"},
		})
	}
	b, err := xml.MarshalIndent(rss, "", "  ")
	if err != nil {
		return nil, err
	}
	return append([]byte(xml.Header), append(b, '\n')...), nil
}
