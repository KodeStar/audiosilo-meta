package model

import "testing"

func TestRecordingIsDramatization(t *testing.T) {
	cases := []struct {
		name string
		rec  Recording
		want bool
	}{
		{"GraphicAudio", Recording{Publisher: "GraphicAudio", Narrators: []string{"alex-hill-knight"}}, true},
		{"Graphic Audio LLC with a trailing space", Recording{Publisher: "Graphic Audio LLC "}, true},
		{"a small producer's full cast", Recording{Publisher: "Corey Press", Narrators: []string{"bea-reader", FullCastPerson}}, true},
		{"a source's own spelling of the credit", Recording{Publisher: "Full Cast Audio", Narrators: []string{"the-full-cast-family"}}, true},
		{"a credit naming its lead", Recording{Publisher: "Corey Press", Narrators: []string{"jared-reinfeldt-full-cast"}}, true},
		{"one reader", Recording{Publisher: "Tantor Audio", Narrators: []string{"bea-reader"}}, false},
		{"a surname only ending in the letters", Recording{Publisher: "Tantor Audio", Narrators: []string{"ann-full-castle"}}, false},
		{"several readers", Recording{Publisher: "Spoken Realms", Narrators: []string{"a", "b", "c", "d", "e"}}, false},
		{"a publisher only naming the words", Recording{Publisher: "Graphic Audio Books of Ohio"}, false},
		{"no statement", Recording{}, false},
	}
	for _, c := range cases {
		if got := c.rec.IsDramatization(); got != c.want {
			t.Errorf("%s: IsDramatization = %v, want %v", c.name, got, c.want)
		}
	}
}
