package check

import (
	"fmt"
	"testing"

	"github.com/kodestar/audiosilo-meta/pkg/model"
)

func tracksOf(titles ...string) []model.Chapter {
	out := make([]model.Chapter, len(titles))
	for i, t := range titles {
		out[i] = model.Chapter{Title: t}
	}
	return out
}

func repeat(format string, n int) []string {
	var out []string
	for i := 1; i <= n; i++ {
		out = append(out, fmt.Sprintf(format, i))
	}
	return out
}

func TestShapeOf(t *testing.T) {
	cat := func(parts ...[]string) []string {
		var out []string
		for _, p := range parts {
			out = append(out, p...)
		}
		return out
	}
	cases := []struct {
		name            string
		titles          []string
		chapters, parts int
	}{
		{"plain chapters", repeat("Chapter %d", 20), 20, 0},
		{"matter is no chapter", cat([]string{"Opening Credits", "Dedication", "Epigraph", "Foreword"},
			repeat("Chapter %d", 20), []string{"Author's Note", "End Credits", "Closing Credits", "Credits"}), 20, 0},
		{"story framing stays chapters", cat([]string{"Prologue", "Introduction"}, repeat("Chapter %d", 3),
			[]string{"Interlude", "Epilogue"}), 7, 0},
		{"part dividers", cat([]string{"Part One: The Traveler"}, repeat("Chapter %d", 3),
			[]string{"Part Two : Red Royal"}, repeat("Chapter %d", 3), []string{"Book III"}, repeat("Chapter %d", 3)), 9, 3},
		{"number-word dividers", cat([]string{"One: Thief at Sea"}, repeat("Chapter %d", 4),
			[]string{"Twenty-One"}, repeat("Chapter %d", 4)), 8, 2},
		{"a track number is no part of the title", cat([]string{"01 - Part One"}, repeat("02 - Chapter %d", 2),
			[]string{"05 - Part Two"}, repeat("06 - Chapter %d", 2)), 4, 2},
		{"parts stated in chapter titles", cat(repeat("Part 1 - Chapter %d", 3), repeat("Part 2 - Chapter %d", 3),
			repeat("Part 3, chapter %d", 2)), 8, 3},
		{"a scene is a chapter track", repeat("Act I, Scene %d", 5), 5, 0},
		{"dividers dividing nothing are the chapters", repeat("Book %d", 24), 24, 0},
		{"number-word chapter titles are chapters", cat(repeat("Chapter %d", 1), []string{"One", "Two", "Three"}), 4, 0},
		{"a lone divider is no part count", cat([]string{"Part IV: Happily Ever After"}, repeat("Chapter %d", 5)), 5, 0},
	}
	for _, c := range cases {
		got := shapeOf(tracksOf(c.titles...))
		if got.chapters != c.chapters || got.parts != c.parts {
			t.Errorf("%s: shapeOf = %d chapters, %d parts; want %d, %d", c.name, got.chapters, got.parts, c.chapters, c.parts)
		}
	}
}
