package model

import "testing"

func TestValidSlug(t *testing.T) {
	cases := map[string]bool{
		"harry-potter": true,
		"a":            true,
		"j-k-rowling":  true,
		"book1":        true,
		"9-lives":      true,
		"":             false,
		"Harry-Potter": false,
		"harry_potter": false,
		"-leading":     false,
		"trailing-":    false,
		"double--dash": false,
		"has space":    false,
		"café":         false,
	}
	for in, want := range cases {
		if got := ValidSlug(in); got != want {
			t.Errorf("ValidSlug(%q) = %v, want %v", in, got, want)
		}
	}
	long := make([]byte, MaxSlugLen+1)
	for i := range long {
		long[i] = 'a'
	}
	if ValidSlug(string(long)) {
		t.Errorf("slug over %d chars should be invalid", MaxSlugLen)
	}
}

func TestPackLocationString(t *testing.T) {
	cases := []struct {
		loc  PackLocation
		want string
	}{
		{
			PackLocation{Kind: KindWork, Family: "works", Pack: "works/0/0.json", Slug: "dune"},
			"works/0/0.json: entry dune",
		},
		{
			PackLocation{Kind: KindRecording, Family: "works", Pack: "works/0/0.json", Slug: "dune", RecSlug: "rec-one"},
			"works/0/0.json: entry dune: recording rec-one",
		},
		{
			PackLocation{Kind: KindCharacters, Family: "works-community", Pack: "works-community/0/0.json", Slug: "dune"},
			"works-community/0/0.json: entry dune",
		},
	}
	for _, c := range cases {
		if got := c.loc.String(); got != c.want {
			t.Errorf("String() = %q, want %q", got, c.want)
		}
	}
}
