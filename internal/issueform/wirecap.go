package issueform

import (
	"fmt"
	"unicode/utf8"
)

// The wire bounds on a verdict's messages (see Result.MarshalJSON). They sit
// well inside GitHub's 65,536-character body ceiling, leaving room for the
// workflow's own framing.
const (
	// maxWireMessages is how many messages the verdict carries.
	maxWireMessages = 50
	// maxWireMessageBytes bounds their total size: fifty long messages break
	// the ceiling as surely as five hundred short ones.
	maxWireMessageBytes = 40 << 10
	// maxOneMessageBytes bounds one message, so a single echo of submitted text
	// cannot spend the whole budget on itself.
	maxOneMessageBytes = 2 << 10
)

// wireMessages is the head of msgs that fits the wire bounds, in order: the
// first maxWireMessages, each cut to maxOneMessageBytes, for as long as the
// total stays within maxWireMessageBytes, then ONE line saying how many were
// left out - unless only ONE was, and showing it costs no more than the line
// that would replace it (it fits the byte budget, or is no longer than that
// line): truncating then saves nothing and hides a message. Keeping the head is
// sound because every producer puts the line a maintainer acts on first (the
// verdict's reason, then the importer's run-level and conflict warnings - see
// importer.planner.result).
func wireMessages(msgs []string) []string {
	var head []string
	used := 0
	for i, m := range msgs {
		m = clipMessage(m)
		if i == maxWireMessages || used+len(m) > maxWireMessageBytes {
			more := fmt.Sprintf("... and %d more (full list in the run log)", len(msgs)-i)
			if i == len(msgs)-1 && (used+len(m) <= maxWireMessageBytes || len(m) <= len(more)) {
				more = m
			}
			return append(head, more)
		}
		used += len(m)
		head = append(head, m)
	}
	return head
}

// clipMessage cuts one message to maxOneMessageBytes at a rune boundary, marking
// the cut, so the bullet it becomes is never invalid UTF-8.
func clipMessage(m string) string {
	if len(m) <= maxOneMessageBytes {
		return m
	}
	const mark = "..."
	end := maxOneMessageBytes - len(mark)
	for end > 0 && !utf8.RuneStart(m[end]) {
		end--
	}
	return m[:end] + mark
}
