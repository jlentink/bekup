package email

import (
	"errors"
	"strings"
	"testing"
)

func TestRenderEscapes(t *testing.T) {
	c, err := Render(Notice{
		Subject:    "Job <b>x</b>",
		Heading:    "Backup of \"prod\" & <script>alert(1)</script>",
		Paragraphs: []string{"<img src=https://tracker.example/p.gif>", "plain"},
		Details:    []Detail{{Label: "Job", Value: "<a href=\"javascript:alert(1)\">x</a>"}},
		Action:     &Action{Label: "Open <bekup>", URL: "https://bekup.example.com/jobs/1?a=1&b=\"2\""},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"<script", "<img", "<b>x</b>", "<a href=\"javascript", "<bekup>"} {
		if strings.Contains(c.HTML, bad) {
			t.Fatalf("HTML contains %q:\n%s", bad, c.HTML)
		}
	}
	for _, want := range []string{"&lt;script&gt;", "&lt;img", `href="https://bekup.example.com/jobs/1?a=1&amp;b=%222%22"`} {
		if !strings.Contains(c.HTML, want) {
			t.Fatalf("HTML lacks %q:\n%s", want, c.HTML)
		}
	}
	// No remote resources at all in the layout.
	for _, bad := range []string{"<img", "<link", "<script", "src=", "url(", "@import"} {
		if strings.Contains(strings.ReplaceAll(c.HTML, "&lt;img src=https://tracker.example/p.gif&gt;", ""), bad) {
			t.Fatalf("HTML references %q", bad)
		}
	}
	if !strings.Contains(c.Text, "https://bekup.example.com/jobs/1?a=1&b=\"2\"") || !strings.Contains(c.Text, "<script>") {
		t.Fatalf("text part should carry values verbatim:\n%s", c.Text)
	}
	if !strings.Contains(c.Text, DefaultFooter) || !strings.Contains(c.HTML, "no tracking") {
		t.Fatal("default footer missing")
	}
}

func TestRenderRejects(t *testing.T) {
	cases := map[string]Notice{
		"no subject":       {Heading: "h"},
		"no heading":       {Subject: "s"},
		"javascript link":  {Subject: "s", Heading: "h", Action: &Action{Label: "x", URL: "javascript:alert(1)"}},
		"data link":        {Subject: "s", Heading: "h", Action: &Action{Label: "x", URL: "data:text/html,x"}},
		"relative link":    {Subject: "s", Heading: "h", Action: &Action{Label: "x", URL: "/jobs/1"}},
		"userinfo link":    {Subject: "s", Heading: "h", Action: &Action{Label: "x", URL: "https://u:p@example.com/"}},
		"no label":         {Subject: "s", Heading: "h", Action: &Action{URL: "https://example.com/"}},
		"newline":          {Subject: "s", Heading: "h", Paragraphs: []string{"a\nb"}},
		"subject newline":  {Subject: "s\r\nBcc: x", Heading: "h"},
		"detail control":   {Subject: "s", Heading: "h", Details: []Detail{{Label: "a", Value: "b\x1bc"}}},
		"too many details": {Subject: "s", Heading: "h", Details: make([]Detail, maxDetails+1)},
	}
	for name, n := range cases {
		if _, err := Render(n); !errors.Is(err, ErrInvalidMessage) {
			t.Errorf("%s: %v", name, err)
		}
	}
}
