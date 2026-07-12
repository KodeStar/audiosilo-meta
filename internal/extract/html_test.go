package extract

import "testing"

func TestHTMLToText(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "paragraphs become separate blocks",
			in:   "<html><body><p>First para.</p><p>Second para.</p></body></html>",
			want: "First para.\n\nSecond para.",
		},
		{
			name: "head content is dropped",
			in:   "<html><head><title>Meta Title</title></head><body><p>Body text.</p></body></html>",
			want: "Body text.",
		},
		{
			name: "style content is dropped",
			in:   "<body><style>.chapter { color: red; }</style><p>Visible.</p></body>",
			want: "Visible.",
		},
		{
			name: "script content is dropped even with stray angle brackets",
			in:   "<body><script>if (a < b) { doThing(); }</script><p>After.</p></body>",
			want: "After.",
		},
		{
			name: "entities are unescaped",
			in:   "<p>Tom &amp; Jerry, don&#39;t &lt;stop&gt;.</p>",
			want: "Tom & Jerry, don't <stop>.",
		},
		{
			name: "non-breaking space becomes a normal space",
			in:   "<p>a&#160;b</p>",
			want: "a b",
		},
		{
			name: "runs of whitespace collapse",
			in:   "<p>lots     of\t\tspace</p>",
			want: "lots of space",
		},
		{
			name: "br breaks a line, headings break a block",
			in:   "<h1>Title</h1><p>Line one<br/>Line two</p>",
			want: "Title\n\nLine one\nLine two",
		},
		{
			name: "comments are ignored",
			in:   "<p>before<!-- a comment with <b>tags</b> -->after</p>",
			want: "beforeafter",
		},
		{
			name: "list items separate",
			in:   "<ul><li>one</li><li>two</li></ul>",
			want: "one\n\ntwo",
		},
		{
			name: "namespaced tags are handled",
			in:   `<body><p>text</p><svg:svg><svg:rect/></svg:svg></body>`,
			want: "text",
		},
		{
			name: "self-closed script does not swallow the rest of the document",
			in:   `<body><script src="x.js"/><p>Body survives.</p></body>`,
			want: "Body survives.",
		},
		{
			name: "self-closed style mid-document keeps following text",
			in:   `<body><p>Before.</p><style/><p>After.</p></body>`,
			want: "Before.\n\nAfter.",
		},
		{
			name: "self-closed head does not suppress the body",
			in:   `<html><head/><body><p>Body survives.</p></body></html>`,
			want: "Body survives.",
		},
		{
			name: "normal script element is still skipped whole",
			in:   `<body><script>document.write("<p>not text</p>");</script><p>Real.</p></body>`,
			want: "Real.",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := htmlToText([]byte(tc.in))
			if got != tc.want {
				t.Errorf("htmlToText()\n got: %q\nwant: %q", got, tc.want)
			}
		})
	}
}

func TestInferChapter(t *testing.T) {
	seven := 7
	tests := []struct {
		label string
		want  *int
	}{
		{"Chapter 7", &seven},
		{"chapter 7", &seven},
		{"CHAPTER 7", &seven},
		{"7", &seven},
		{"Epilogue", nil},
		{"Chapter Seven", nil},
		{"Chapter 7: The Return", nil},
		{"Part 7", nil},
		{"", nil},
	}
	for _, tc := range tests {
		got := inferChapter(tc.label)
		switch {
		case tc.want == nil && got != nil:
			t.Errorf("inferChapter(%q) = %d, want nil", tc.label, *got)
		case tc.want != nil && got == nil:
			t.Errorf("inferChapter(%q) = nil, want %d", tc.label, *tc.want)
		case tc.want != nil && got != nil && *got != *tc.want:
			t.Errorf("inferChapter(%q) = %d, want %d", tc.label, *got, *tc.want)
		}
	}
}

func TestResolveHref(t *testing.T) {
	tests := []struct {
		base, href string
		want       string
		wantErr    bool
	}{
		{"OEBPS", "ch01.xhtml", "OEBPS/ch01.xhtml", false},
		{"OEBPS", "ch01.xhtml#part2", "OEBPS/ch01.xhtml", false},
		{"OEBPS/text", "../images/x.jpg", "OEBPS/images/x.jpg", false},
		{"", "content.opf", "content.opf", false},
		{"OEBPS", "sub%20dir/a.xhtml", "OEBPS/sub dir/a.xhtml", false},
		{"OEBPS", "../../etc/passwd", "", true},
		{"OEBPS", "/etc/passwd", "", true},
		{"OEBPS", "", "", true},
		{"OEBPS", "#justafragment", "", true},
	}
	for _, tc := range tests {
		got, err := resolveHref(tc.base, tc.href)
		if tc.wantErr {
			if err == nil {
				t.Errorf("resolveHref(%q,%q) = %q, want error", tc.base, tc.href, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("resolveHref(%q,%q) unexpected error: %v", tc.base, tc.href, err)
			continue
		}
		if got != tc.want {
			t.Errorf("resolveHref(%q,%q) = %q, want %q", tc.base, tc.href, got, tc.want)
		}
	}
}
