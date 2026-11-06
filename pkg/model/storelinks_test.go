package model

import (
	"encoding/json"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestStoreLinkOK(t *testing.T) {
	cases := []struct {
		store, url string
		want       bool
	}{
		{StoreAudiobookGuild, "https://audiobookguild.com/products/without-law-17", true},
		{StoreAudiobookGuild, "https://www.audiobookguild.com/products/without-law-17", true},
		{StoreAudiobookGuild, "https://AudiobookGuild.com/products/x", true},
		{StoreGraphicAudio, "https://www.graphicaudio.net/mistborn-1-the-final-empire-1-of-3.html", true},
		{StoreGraphicAudio, "https://graphicaudio.net/x.html", true},
		// The other store's host, a look-alike, a subdomain, http, an upper-case
		// scheme (the schema's https_url is the literal "https://"), a port,
		// credentials, a relative URL and an unknown store are all refused.
		{StoreAudiobookGuild, "https://www.graphicaudio.net/x.html", false},
		{StoreGraphicAudio, "https://audiobookguild.com/products/x", false},
		{StoreAudiobookGuild, "https://audiobookguild.com.evil.example/products/x", false},
		{StoreAudiobookGuild, "https://shop.audiobookguild.com/products/x", false},
		{StoreAudiobookGuild, "http://audiobookguild.com/products/x", false},
		{StoreAudiobookGuild, "HTTPS://audiobookguild.com/products/x", false},
		{StoreAudiobookGuild, "https://audiobookguild.com:8443/products/x", false},
		{StoreAudiobookGuild, "https://user@audiobookguild.com/products/x", false},
		{StoreAudiobookGuild, "/products/x", false},
		{"audible", "https://www.audible.com/pd/B000000000", false},
		// StoreLinkKey is the URL itself, so a query string or fragment (even an
		// empty one) would make one page two keys; a bare homepage is no book.
		{StoreAudiobookGuild, "https://audiobookguild.com/products/x?variant=1", false},
		{StoreAudiobookGuild, "https://audiobookguild.com/products/x?", false},
		{StoreGraphicAudio, "https://www.graphicaudio.net/x.html#reviews", false},
		{StoreGraphicAudio, "https://www.graphicaudio.net/x.html#", false},
		{StoreAudiobookGuild, "https://audiobookguild.com", false},
		{StoreAudiobookGuild, "https://audiobookguild.com/", false},
		{StoreGraphicAudio, "https://www.graphicaudio.net/", false},
	}
	for _, c := range cases {
		if got := StoreLinkOK(c.store, c.url); got != c.want {
			t.Errorf("StoreLinkOK(%q, %q) = %v, want %v", c.store, c.url, got, c.want)
		}
	}
}

func TestRecordingLinksStoreAccessors(t *testing.T) {
	var nilLinks *RecordingLinks
	var l RecordingLinks
	for _, key := range StoreKeys() {
		if !l.SetStore(key, "https://example/"+key) {
			t.Errorf("SetStore(%q) refused a store key", key)
		}
	}
	if before := l; l.SetStore("audible", "x") || l != before {
		t.Error("an unknown store key must be refused and change nothing")
	}
	var got []string
	for key, link := range l.StoreLinks() {
		got = append(got, key+"="+link)
	}
	if want := []string{"audiobookguild=https://example/audiobookguild", "graphicaudio=https://example/graphicaudio"}; !slices.Equal(got, want) {
		t.Errorf("StoreLinks() = %v, want %v", got, want)
	}
	for range nilLinks.StoreLinks() {
		t.Error("a nil links object yields no store link")
	}
}

// TestStoreKeysMatchTheSchema keeps the store vocabulary, the RecordingLinks
// JSON tags and recording.schema.json's links properties in step: every store
// key is a links property and a struct field, and every links property that is
// not a LibriVox link is a store key.
func TestStoreKeysMatchTheSchema(t *testing.T) {
	raw, err := os.ReadFile("../../schema/recording.schema.json")
	if err != nil {
		t.Fatalf("read recording.schema.json: %v", err)
	}
	var doc struct {
		Properties struct {
			Links struct {
				Properties map[string]json.RawMessage `json:"properties"`
			} `json:"links"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse recording.schema.json: %v", err)
	}
	librivox := []string{"librivox", "zip", "rss", "text"}
	props := doc.Properties.Links.Properties
	for key := range props {
		if !slices.Contains(librivox, key) && !KnownStore(key) {
			t.Errorf("links property %q is neither a LibriVox link nor a store key (pkg/model/storelinks.go)", key)
		}
	}
	tags := map[string]bool{}
	rt := reflect.TypeFor[RecordingLinks]()
	for i := range rt.NumField() {
		name, _, _ := strings.Cut(rt.Field(i).Tag.Get("json"), ",")
		tags[name] = true
	}
	for _, key := range StoreKeys() {
		if _, ok := props[key]; !ok {
			t.Errorf("store key %q is not a links property in recording.schema.json", key)
		}
		if !tags[key] {
			t.Errorf("store key %q is not a RecordingLinks JSON key", key)
		}
		if len(StoreHosts(key)) == 0 {
			t.Errorf("store key %q names no host", key)
		}
	}
}

func TestStoreLinkKey(t *testing.T) {
	same := []string{
		"https://audiobookguild.com/products/without-law-17",
		"https://www.audiobookguild.com/products/without-law-17",
		"https://WWW.AudiobookGuild.com/products/without-law-17",
	}
	for _, l := range same {
		if got := StoreLinkKey(l); got != "audiobookguild.com/products/without-law-17" {
			t.Errorf("StoreLinkKey(%q) = %q", l, got)
		}
	}
	// The path is the store's: its case and a query are kept.
	if StoreLinkKey("https://audiobookguild.com/products/Without-Law-17") == StoreLinkKey(same[0]) {
		t.Error("a path differing in case must key apart")
	}
	if got := StoreLinkKey("http://audiobookguild.com/x"); got != "http://audiobookguild.com/x" {
		t.Errorf("a non-https link keys as itself, got %q", got)
	}
}
