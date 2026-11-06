package check

import (
	"strings"
	"testing"
)

// storeRecording is baseValid's recording carrying links, in canonical
// (sorted-key) form, stamped from a store listing.
func storeRecording(id, links string) string {
	return `{"id":"` + id + `","language":"en","license":"CC0-1.0","links":` + links +
		`,"narrators":["narrator-one"],"sources":[{"imported_at":"2026-11-06","ref":"https://audiobookguild.com/products/book-one","type":"store-listing"}],"work":"book-one"}`
}

func TestStoreLinksValid(t *testing.T) {
	files := baseValid()
	files["works/bo/book-one/recordings/rec-one.json"] = storeRecording("rec-one",
		`{"audiobookguild":"https://audiobookguild.com/products/book-one"}`)
	files["works/bo/book-one/recordings/rec-two.json"] = storeRecording("rec-two",
		`{"graphicaudio":"https://www.graphicaudio.net/book-one-1-of-3.html"}`)
	res := loadTree(t, files)
	if !res.OK() {
		t.Fatalf("store links should validate, got:\n%s", joinProblems(res.Problems))
	}
	got := map[string]string{}
	for _, r := range res.Catalog.Works[0].Recordings {
		got[r.ID] = r.Links.AudiobookGuild + r.Links.GraphicAudio
	}
	if got["rec-one"] != "https://audiobookguild.com/products/book-one" || got["rec-two"] != "https://www.graphicaudio.net/book-one-1-of-3.html" {
		t.Errorf("store links did not load: %v", got)
	}
}

func TestStoreLinksRefused(t *testing.T) {
	cases := []struct {
		name  string
		links string
		want  string
	}{
		{"another store's host", `{"audiobookguild":"https://www.graphicaudio.net/book-one.html"}`, `links.audiobookguild "https://www.graphicaudio.net/book-one.html" is not an https page on audiobookguild.com or www.audiobookguild.com`},
		{"a query string", `{"audiobookguild":"https://audiobookguild.com/products/book-one?variant=2"}`, `links.audiobookguild "https://audiobookguild.com/products/book-one?variant=2" is not an https page on`},
		{"a fragment", `{"graphicaudio":"https://www.graphicaudio.net/book-one.html#reviews"}`, `links.graphicaudio "https://www.graphicaudio.net/book-one.html#reviews" is not an https page on`},
		{"a bare homepage", `{"graphicaudio":"https://www.graphicaudio.net/"}`, `links.graphicaudio "https://www.graphicaudio.net/" is not an https page on`},
		{"no path at all", `{"audiobookguild":"https://audiobookguild.com"}`, `links.audiobookguild "https://audiobookguild.com" is not an https page on`},
		{"a look-alike host", `{"graphicaudio":"https://graphicaudio.net.example.com/x.html"}`, `links.graphicaudio "https://graphicaudio.net.example.com/x.html" is not an https page on graphicaudio.net or www.graphicaudio.net`},
		{"plain http (the schema)", `{"audiobookguild":"http://audiobookguild.com/products/book-one"}`, `audiobookguild`},
		{"an unknown store key (the schema)", `{"audiobooksnow":"https://www.audiobooksnow.com/audiobook/x/1/"}`, `audiobooksnow`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			files := baseValid()
			files["works/bo/book-one/recordings/rec-one.json"] = storeRecording("rec-one", c.links)
			res := loadTree(t, files)
			if res.OK() || !strings.Contains(joinProblems(res.Problems), c.want) {
				t.Fatalf("want a problem containing %q, got:\n%s", c.want, joinProblems(res.Problems))
			}
		})
	}
}

// TestStoreLinksUnique: one store page on two recordings is refused, whichever
// spelling of the host each states.
func TestStoreLinksUnique(t *testing.T) {
	files := baseValid()
	files["works/bo/book-one/recordings/rec-one.json"] = storeRecording("rec-one",
		`{"audiobookguild":"https://audiobookguild.com/products/book-one"}`)
	files["works/bo/book-one/recordings/rec-two.json"] = storeRecording("rec-two",
		`{"audiobookguild":"https://WWW.audiobookguild.com/products/book-one"}`)
	res := loadTree(t, files)
	if res.OK() || !strings.Contains(joinProblems(res.Problems), `duplicate links.audiobookguild "https://WWW.audiobookguild.com/products/book-one" also in`) {
		t.Fatalf("want a duplicate store link problem, got:\n%s", joinProblems(res.Problems))
	}
}

// A trailing "/" is the same store page, so it is the same link
// (model.StoreLinkKey folds it).
func TestStoreLinksUniqueTrailingSlash(t *testing.T) {
	files := baseValid()
	files["works/bo/book-one/recordings/rec-one.json"] = storeRecording("rec-one",
		`{"audiobookguild":"https://audiobookguild.com/products/book-one"}`)
	files["works/bo/book-one/recordings/rec-two.json"] = storeRecording("rec-two",
		`{"audiobookguild":"https://audiobookguild.com/products/book-one/"}`)
	res := loadTree(t, files)
	if res.OK() || !strings.Contains(joinProblems(res.Problems), `duplicate links.audiobookguild "https://audiobookguild.com/products/book-one/" also in`) {
		t.Fatalf("want a duplicate store link problem, got:\n%s", joinProblems(res.Problems))
	}
}
