package issueform

import (
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/kodestar/audiosilo-meta/pkg/check"
	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// schemaVocabURL is where a contributor reads the shared controlled vocabularies
// (common.schema.json's $defs), named when a list is too long to quote in a
// verdict - the genre list runs to dozens of values.
const schemaVocabURL = "https://github.com/kodestar/audiosilo-meta/blob/main/schema/common.schema.json"

// recordFields maps every kind a correction can address to its schema's field
// table (check.FieldsOf, read off the compiled schemas). The kinds are
// correctableFields' own keys, so the two cannot disagree about which records a
// correction reaches. The schemas are embedded, so a failure is a programming
// error (TestRecordFieldsReadTheSchemas) and panics rather than silently
// treating every field as unknown.
var recordFields = sync.OnceValue(func() map[model.Kind]map[string]check.Field {
	out := make(map[model.Kind]map[string]check.Field, len(correctableFields))
	for kind := range correctableFields {
		fields, err := check.FieldsOf(kind)
		if err != nil {
			panic(fmt.Sprintf("issueform: embedded record schemas are unusable: %v", err))
		}
		out[kind] = fields
	}
	return out
})

// enumViolation names why corrected cannot be written to field on a record of
// this kind - a value outside the schema's closed vocabulary - or returns "".
// Such a correction can never be applied, by a maintainer or anyone else, so it
// is refused as the submitter's to fix rather than parked for a human.
//
// The comparison ignores case: "Publisher" names the enum's "publisher" (the
// person-kind coercion lowercases it on the way in), and a value that differs
// only in case is a spelling question, not an unknown value.
func enumViolation(kind model.Kind, field, corrected string) string {
	f := recordFields()[kind][field]
	switch {
	case f.Enum != nil:
		if !containsFold(f.Enum, corrected) {
			return fmt.Sprintf("%q is not an allowed value for %q - the schema allows only %s", corrected, field, allowedValues(f.Enum))
		}
	case f.ItemEnum != nil:
		for _, v := range splitList(corrected) {
			if !containsFold(f.ItemEnum, v) {
				return fmt.Sprintf("%q is not an allowed value for %q - each entry must be %s", v, field, allowedValues(f.ItemEnum))
			}
		}
	}
	return ""
}

// allowedValues renders a vocabulary for a verdict: quoted in full when it is
// short enough to read, pointed at when it is not.
func allowedValues(vals []string) string {
	if len(vals) > 12 {
		return fmt.Sprintf("one of the %d values listed in %s", len(vals), schemaVocabURL)
	}
	quoted := make([]string, len(vals))
	for i, v := range vals {
		quoted[i] = fmt.Sprintf("%q", v)
	}
	return strings.Join(quoted, ", ")
}

// containsFold reports whether s names one of vals, ignoring case. Callers pass
// values the form parser has already trimmed.
func containsFold(vals []string, s string) bool {
	return slices.ContainsFunc(vals, func(v string) bool { return strings.EqualFold(v, s) })
}

// enumSpelling returns raw in the spelling the vocabulary gives it, or raw
// unchanged when vals is nil or does not name it. enumViolation has already
// refused a value outside the vocabulary, so this only ever settles case.
func enumSpelling(vals []string, raw string) string {
	for _, v := range vals {
		if strings.EqualFold(v, raw) {
			return v
		}
	}
	return raw
}
