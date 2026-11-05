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
		{"one reader", Recording{Publisher: "Tantor Audio", Narrators: []string{"bea-reader"}}, false},
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
