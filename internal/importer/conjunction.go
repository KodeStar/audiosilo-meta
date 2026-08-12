package importer

import "strings"

// conjunction.go drops a leading co-credit conjunction a source left dangling on
// a name: "and Melanie Mendez", "and Ava Erickson", "and Soneela Nankani" - the
// tail end of "<narrator>, <narrator> and <narrator>" split one element too
// late. Without a rule each mints and-melanie-mendez beside the narrator's own
// record (issue #1800).
//
// It is a PREFIX rule, so it looks like prefixCredits (mapping.go) - and it
// deliberately is not one. Those entries are whole credit PHRASES ("Created
// by ", "Read by ") that state something; a bare "and" states nothing and is not
// even evidence that what follows is a name. So the rule carries census evidence
// instead, and both of its guards are load-bearing. Measured over the full
// 1.13M-book dump, 20 distinct credit names begin with "and ":
//
//	8 fire  and Ava Erickson, and Erin Mallon, and Lili Valente, and Melanie
//	    Mendez, and Soneela Nankani, and Tim Sample, and David Meerman Scott,
//	    and various narrators (which then folds onto Various, collective.go).
//	    Every one is correct.
//	4 are stopped by the WORD FLOOR  "and more", "and others", "and Others",
//	    "and Friends". Their remainders are one word AND are attested as credits,
//	    so the census alone would have stripped them onto a person called "more".
//	8 are stopped by the CENSUS  "And We Know" (a channel whose name begins with
//	    the word), "and the Axis Team with Sarah Miller", "and 19 other authors",
//	    "und vielen anderen", and four real names nobody credited bare in the
//	    dump ("and Christopher Wall", "and Cynthia Rudigier").
//
// "And Arndt." - a corrupted "Andi Arndt", named in the issue - is stopped by
// BOTH guards, and that is the right outcome rather than a missed one. Stripping
// would leave the one-word "Arndt.", a surname fragment that is not the narrator
// and is not anybody; refusing the ROW would drop a real book over a spelling
// nothing else about it is wrong. It imports as the source spelled it and is a
// hand-remediation item, exactly like "A.J Watkins of Books.Audio"
// (studiotail.go).
//
// The census is the SAME-SIDE one: the question is whether the remainder is a
// name credited for this kind of work, and the any-side universe would gain
// nothing measurable here (all four unattested remainders are unattested on both
// sides) while widening the shape that has to be trusted.
//
// German "und " is deliberately not read: the dump attests exactly one name
// ("und vielen anderen"), whose remainder is a collective statement rather than
// a person, so there is nothing to strip it FOR.

// leadingConjunction is the one prefix this rule reads. It keeps its trailing
// space, so a name merely starting with the letters ("Andrea Lodge") is
// untouched; matching is case-insensitive (the dump spells it both ways).
const leadingConjunction = "and "

// minDeConjoinedWords is the smallest remainder the rule will accept. See the
// word-floor paragraph in this file's header - "and more" is why it exists.
const minDeConjoinedWords = 2

// deConjoined strips a leading dangling conjunction, reporting whether one was
// there and left an acceptable full name behind.
func deConjoined(name string) (rest string, stripped bool) {
	rest, ok := cutPrefixFold(strings.TrimSpace(name), leadingConjunction)
	if !ok {
		return "", false
	}
	rest = strings.TrimSpace(rest)
	if len(strings.Fields(rest)) < minDeConjoinedWords {
		return "", false
	}
	return rest, true
}

// stripLeadingConjunction drops the dangling conjunction when the census has
// already seen the remainder as a credit on this side. A nil census has seen
// nothing, so the rule never fires for a caller with no catalogue in hand - the
// bootstrap honesty every census-backed rule here keeps.
func stripLeadingConjunction(name string, seen creditSeenFunc) string {
	return stripOntoSeenTwin(name, seen, deConjoined)
}
