package model

import "regexp"

// FullCastPerson is the canonical person record a "Full Cast" credit folds onto.
const FullCastPerson = "full-cast"

// graphicAudioPublisher is every spelling the catalogue and libex record
// GraphicAudio's imprint under: "GraphicAudio", "Graphic Audio", "GraphicAudio LLC"
// and "Graphic Audio LLC", in any case, trailing space included.
var graphicAudioPublisher = regexp.MustCompile(`(?i)^\s*graphic\s?audio(\s+llc)?\s*$`)

// IsDramatizationPublisher reports whether a publisher name is a producer whose
// whole list is dramatized productions.
func IsDramatizationPublisher(publisher string) bool {
	return graphicAudioPublisher.MatchString(publisher)
}

// IsDramatization reports whether a recording states it is a dramatized production:
// GraphicAudio's imprint, or a full-cast credit, which is how a small producer's
// production says so. A dramatization is a recording of its book, so this is a fact
// about the RECORDING and no work's title decides it. Several named readers are not
// enough: a chapter-by-chapter volunteer reading credits them too.
func (r *Recording) IsDramatization() bool {
	if IsDramatizationPublisher(r.Publisher) {
		return true
	}
	for _, n := range r.Narrators {
		if n == FullCastPerson {
			return true
		}
	}
	return false
}
