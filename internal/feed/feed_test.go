package feed

import (
	"encoding/xml"
	"strings"
	"testing"
	"time"

	"github.com/kkato/radipod/internal/store"
)

func TestArtworkUsesRSSAndITunesNamespace(t *testing.T) {
	cover := "https://images.example/cover.png?size=1024&version=2"
	episodes := []store.Episode{{ID: "TBS_20261011100000", Title: "番組", Start: time.Now(), Size: 123}}
	data, err := Generate(episodes, "https://radio.example", "私の & ラジオ", cover)
	if err != nil {
		t.Fatal(err)
	}
	// Parse by namespace URI, just as a podcast reader does, rather than
	// matching the literal prefix used by our encoder.
	var rss struct {
		Channel struct {
			ITunesImage ITunesImage `xml:"http://www.itunes.com/dtds/podcast-1.0.dtd image"`
			Image       Image       `xml:"image"`
			Items       []Item      `xml:"item"`
		} `xml:"channel"`
	}
	if err := xml.Unmarshal(data, &rss); err != nil {
		t.Fatal(err)
	}
	if rss.Channel.Image.URL != cover || rss.Channel.ITunesImage.Href != cover || rss.Channel.Image.Title != "私の & ラジオ" || rss.Channel.Image.Link != "https://radio.example/" {
		t.Fatalf("artwork missing or corrupted: %s", data)
	}
	without, err := Generate(episodes, "https://radio.example", "私の & ラジオ", "")
	if err != nil {
		t.Fatal(err)
	}
	var old RSS
	if err := xml.Unmarshal(without, &old); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(without), "<image>") || strings.Contains(string(without), "itunes") {
		t.Fatalf("empty artwork emitted: %s", without)
	}
	if rss.Channel.Items[0].GUID != old.Channel.Items[0].GUID || rss.Channel.Items[0].Enclosure != old.Channel.Items[0].Enclosure {
		t.Fatal("changing cover changed episode identity or media")
	}
}

func TestArtworkRejectsInvalidURLs(t *testing.T) {
	for _, u := range []string{"/cover.jpg", "file:///cover.jpg", "data:image/png;base64,x", "https://user:secret@example.com/image.png", "https://example.com/image.png#frag", "https://", " "} {
		if _, err := Generate(nil, "https://radio.example", "test", u); err == nil {
			t.Errorf("accepted invalid image URL %q", u)
		}
	}
}
