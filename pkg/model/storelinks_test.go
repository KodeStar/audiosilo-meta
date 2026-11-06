package model

import (
	"encoding/json"
	"os"
	"reflect"
	"slices"
	"testing"
)

func TestStoreLinkHostOK(t *testing.T) {
	cases := []struct {
		store, url string
		want       bool
	}{
		{StoreAudiobookGuild, "https://audiobookguild.com/products/without-law-17", true},
		{StoreAudiobookGuild, "https://www.audiobookguild.com/products/without-law-17", true},
		{StoreAudiobookGuild, "https://AudiobookGuild.com/products/x", true},
		{StoreGraphicAudio, "https://www.graphicaudio.net/mistborn-1-the-final-empire-1-of-3.html", true},
		{StoreGraphicAudio, "https://graphicaudio.net/x.html", true},
		// The other store's host, a look-alike, a subdomain, http, a port,
		// credentials, a relative URL and an unknown store are all refused.
		{StoreAudiobookGuild, "https://www.graphicaudio.net/x.html", false},
		{StoreGraphicAudio, "https://audiobookguild.com/products/x", false},
		{StoreAudiobookGuild, "https://audiobookguild.com.evil.example/products/x", false},
		{StoreAudiobookGuild, "https://shop.audiobookguild.com/products/x", false},
		{StoreAudiobookGuild, "http://audiobookguild.com/products/x", false},
		{StoreAudiobookGuild, "https://audiobookguild.com:8443/products/x", false},
		{StoreAudiobookGuild, "https://user@audiobookguild.com/products/x", false},
		{StoreAudiobookGuild, "/products/x", false},
		{"audible", "https://www.audible.com/pd/B000000000", false},
	}
	for _, c := range cases {
		if got := StoreLinkHostOK(c.store, c.url); got != c.want {
			t.Errorf("StoreLinkHostOK(%q, %q) = %v, want %v", c.store, c.url, got, c.want)
		}
	}
}

func TestRecordingLinksStoreAccessors(t *testing.T) {
	var nilLinks *RecordingLinks
	if nilLinks.Store(StoreAudiobookGuild) != "" {
		t.Error("a nil links object states no store link")
	}
	var l RecordingLinks
	for _, key := range StoreKeys() {
		if !l.SetStore(key, "https://example/"+key) || l.Store(key) != "https://example/"+key {
			t.Errorf("SetStore/Store(%q) did not round-trip: %+v", key, l)
		}
	}
	if l.SetStore("audible", "x") || l.Store("audible") != "" {
		t.Error("an unknown store key must be refused")
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
		tag := rt.Field(i).Tag.Get("json")
		tags[tag[:len(tag)-len(",omitempty")]] = true
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
	if len(StoreKeys()) != len(storeHosts) {
		t.Errorf("StoreKeys() lists %d stores, storeHosts holds %d", len(StoreKeys()), len(storeHosts))
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
