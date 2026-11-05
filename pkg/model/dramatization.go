package model

import (
	"regexp"
	"slices"
	"strings"
)

// FullCastPerson is the canonical person record a "Full Cast" credit folds onto.
const FullCastPerson = "full-cast"

// IsFullCastCredit reports whether a person id is a full-cast credit: the canonical
// record, or one a source spelled around it that the credit fold leaves as it was ("the
// Full Cast Family", "Full Cast featuring Joan Walker", "Jared Reinfeldt full cast").
// pkg/query's matchRuntimesSQL asks the same of a narrator row.
func IsFullCastCredit(personID string) bool {
	return strings.Contains("-"+personID+"-", "-"+FullCastPerson+"-")
}

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
	return IsDramatizationPublisher(r.Publisher) || slices.ContainsFunc(r.Narrators, IsFullCastCredit)
}
