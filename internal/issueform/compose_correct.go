package issueform

import (
	"strconv"
	"strings"

	"github.com/kodestar/audiosilo-meta/pkg/model"
	"github.com/kodestar/audiosilo-meta/pkg/pack"
)

// Field labels for correct-data.yml.
const (
	fCorrectRecord    = "Record"
	fCorrectField     = "Field"
	fCorrectCorrected = "Corrected value"
	fCorrectEvidence  = "Evidence / source"
)

// fieldKind classifies a correctable scalar field so the corrector coerces the
// value to the right JSON type before writing it.
type fieldKind int

const (
	kindString fieldKind = iota
	kindInt
	kindBool
	kindLanguage
	kindDateYear
	kindDateFlex
	kindHTTPSURL
)

// correctableFields is the allowlist of single, scalar fields a correction may
// touch per entity kind. Anything not listed (arrays, xref objects, sources,
// series works) needs a human. Keys are the schema field names; values name the
// coercion. Synonyms are handled by normalizeFieldName.
var correctableFields = map[model.Kind]map[string]fieldKind{
	model.KindWork: {
		"title": kindString, "subtitle": kindString,
		"language": kindLanguage, "first_published": kindDateYear,
	},
	model.KindRecording: {
		"publisher": kindString, "runtime_min": kindInt,
		"release_date": kindDateFlex, "cover_url": kindHTTPSURL,
		"abridged": kindBool, "language": kindLanguage,
	},
	model.KindPerson: {
		"name": kindString, "sort_name": kindString, "description": kindString,
	},
	model.KindSeries: {
		"name": kindString,
	},
}

// fieldSynonyms maps a few common field-name spellings onto their schema name.
var fieldSynonyms = map[string]string{
	"runtime": "runtime_min", "runtime_minutes": "runtime_min",
	"cover": "cover_url", "cover_image": "cover_url", "cover_image_url": "cover_url",
	"released": "release_date", "publication_year": "first_published",
	"first_published_year": "first_published",
}

// correctData applies a single-field correction to an existing record, or bails
// to needs-human when the field is not a cleanly-mappable scalar.
//
// A recording correction edits the recording INSIDE its work's composite entry
// and writes the whole entry back, which is the granularity the pack store
// stores; every other kind is an entry of its own.
func (c *composer) correctData(s sections) {
	if !s.checked(fCC0) {
		c.fail(StatusInvalid, "the CC0 public-domain dedication checkbox is not ticked")
		return
	}
	loc, ok := resolveRecordRef(s.get(fCorrectRecord))
	if !ok {
		c.fail(StatusNeedsHuman, "could not resolve %q to a record in data/ - a maintainer will locate it", s.get(fCorrectRecord))
		return
	}
	addr, ok := entryAddress(loc)
	if !ok {
		c.fail(StatusNeedsHuman, "corrections to a %s record are not auto-applied - a maintainer will handle it", loc.Kind)
		return
	}

	fieldName := normalizeFieldName(s.get(fCorrectField))
	corrected := s.get(fCorrectCorrected)
	evidence := s.get(fCorrectEvidence)
	if fieldName == "" || corrected == "" || evidence == "" {
		c.fail(StatusInvalid, "Field, Corrected value, and Evidence are all required")
		return
	}

	kinds, ok := correctableFields[loc.Kind]
	if !ok {
		c.fail(StatusNeedsHuman, "corrections to a %s record are not auto-applied - a maintainer will handle it", loc.Kind)
		return
	}
	kind, ok := kinds[fieldName]
	if !ok {
		c.fail(StatusNeedsHuman, "field %q on a %s cannot be auto-corrected (only simple scalar fields are) - a maintainer will apply it", fieldName, loc.Kind)
		return
	}

	value, ok := coerceFieldValue(kind, corrected)
	if !ok {
		c.fail(StatusInvalid, "corrected value %q is not valid for field %q", corrected, fieldName)
		return
	}

	// entry is the unit the store writes; record is the object being corrected,
	// which for a recording is a live sub-map of entry.
	entry, found, ok := c.entryRaw(addr.family, addr.slug)
	if !ok {
		return
	}
	if !found {
		c.fail(StatusNeedsHuman, "record %s does not exist - a maintainer will locate the right record", addr.label(c))
		return
	}
	record := entry
	if addr.recSlug != "" {
		recs, _ := entry["recordings"].(map[string]any)
		record, _ = recs[addr.recSlug].(map[string]any)
		if record == nil {
			c.fail(StatusNeedsHuman, "record %s does not exist - a maintainer will locate the right record", addr.label(c))
			return
		}
	}
	record[fieldName] = value

	// Record the correction's provenance so the source can be audited later.
	record["sources"] = appendSource(record["sources"], map[string]any{
		"type": sourceUser, "ref": evidence, "imported_at": c.date,
	})

	if !c.putEntry(addr.family, addr.slug, entry) {
		return
	}
	c.note("applied %s = %v on %s", fieldName, value, addr.label(c))
}

// entryAddr is an entity's address in the pack layout: the family and entry key
// it lives under, plus the recordings-map key when it is a recording nested in
// its work's composite entry.
type entryAddr struct {
	family  pack.Family
	slug    string
	recSlug string
}

// label renders the address for a human-facing message.
func (a entryAddr) label(c *composer) string {
	return c.entryLocation(a.family, a.slug, a.recSlug)
}

// entryAddress maps a parsed record reference onto its pack address. ok is false
// for a kind no correction may touch.
func entryAddress(loc model.Location) (entryAddr, bool) {
	switch loc.Kind {
	case model.KindWork:
		return entryAddr{family: pack.FamilyWorks, slug: loc.Slug}, true
	case model.KindRecording:
		return entryAddr{family: pack.FamilyWorks, slug: loc.WorkSlug, recSlug: loc.Slug}, true
	case model.KindPerson:
		return entryAddr{family: pack.FamilyPeople, slug: loc.Slug}, true
	case model.KindSeries:
		return entryAddr{family: pack.FamilySeries, slug: loc.Slug}, true
	default:
		// The CC BY-SA sidecars: community prose, never a scalar correction.
		return entryAddr{}, false
	}
}

// normalizeFieldName lowercases a field name, turns spaces into underscores, and
// resolves a small set of synonyms onto the schema field.
func normalizeFieldName(name string) string {
	n := strings.ToLower(strings.TrimSpace(name))
	n = strings.ReplaceAll(n, " ", "_")
	n = strings.ReplaceAll(n, "-", "_")
	if syn, ok := fieldSynonyms[n]; ok {
		return syn
	}
	return n
}

// coerceFieldValue converts a form string into the JSON value for its field
// kind, validating against the schema constraints.
func coerceFieldValue(kind fieldKind, raw string) (any, bool) {
	raw = strings.TrimSpace(raw)
	switch kind {
	case kindString:
		if raw == "" {
			return nil, false
		}
		return raw, true
	case kindInt:
		n, err := strconv.Atoi(raw)
		if err != nil || n <= 0 {
			return nil, false
		}
		return n, true
	case kindBool:
		switch strings.ToLower(raw) {
		case "true", "abridged", "yes":
			return true, true
		case "false", "unabridged", "no":
			return false, true
		}
		return nil, false
	case kindLanguage:
		return normalizeLanguage(raw)
	case kindDateYear:
		if dateYearRE.MatchString(raw) {
			return raw, true
		}
		return nil, false
	case kindDateFlex:
		if dateFlexRE.MatchString(raw) {
			return raw, true
		}
		return nil, false
	case kindHTTPSURL:
		if strings.HasPrefix(raw, "https://") {
			return raw, true
		}
		return nil, false
	}
	return nil, false
}

// appendSource appends a source object to an existing (possibly nil) sources
// array read from a raw record.
func appendSource(existing any, src map[string]any) []any {
	arr, _ := existing.([]any)
	return append(arr, src)
}
