package news

import "testing"

func TestNormalizeEpisode(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want string
	}{
		{"swd-slug", []string{"notes-of-gooaye-ep-677"}, "gooaye-ep-677"},
		{"swd-url", []string{"https://socialworkerdaily.com/notes-of-gooaye-ep-676/"}, "gooaye-ep-676"},
		{"title-EP", []string{"股癌筆記EP677"}, "gooaye-ep-677"},
		{"spaced-EP", []string{"股癌 EP 675 筆記"}, "gooaye-ep-675"},
		{"leading-zeros", []string{"EP007"}, "gooaye-ep-7"},
		{"slug-wins-over-title", []string{"notes-of-gooaye-ep-100", "EP999"}, "gooaye-ep-100"},
		{"none", []string{"隨便一篇文章"}, ""},
	}
	for _, c := range cases {
		if got := NormalizeEpisode(c.in...); got != c.want {
			t.Errorf("%s: NormalizeEpisode(%v) = %q want %q", c.name, c.in, got, c.want)
		}
	}
}

func TestEpisodeNumber(t *testing.T) {
	if got := EpisodeNumber("gooaye-ep-677"); got != 677 {
		t.Errorf("EpisodeNumber = %d want 677", got)
	}
	if got := EpisodeNumber(""); got != 0 {
		t.Errorf("empty EpisodeNumber = %d want 0", got)
	}
}

func TestPlainText(t *testing.T) {
	in := `<p>被動元件<strong>國巨</strong></p><ul><li>CCL 資金輪動</li></ul>&amp; more`
	got := PlainText(in)
	if got == "" {
		t.Fatal("PlainText returned empty")
	}
	// tags gone, entity unescaped, block boundaries became newlines.
	for _, bad := range []string{"<p>", "<strong>", "<li>", "&amp;"} {
		if contains(got, bad) {
			t.Errorf("PlainText left %q in output: %q", bad, got)
		}
	}
	if !contains(got, "國巨") || !contains(got, "CCL 資金輪動") || !contains(got, "& more") {
		t.Errorf("PlainText dropped content: %q", got)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
