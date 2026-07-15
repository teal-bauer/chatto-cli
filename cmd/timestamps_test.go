package cmd

import (
	"testing"
	"time"
)

func TestFormatMessageTimestamp(t *testing.T) {
	cases := []struct {
		name         string
		epochSeconds int64
		loc          *time.Location
		want         string
	}{
		{
			name:         "UTC",
			epochSeconds: 1745764200, // 2025-04-27T14:30:00Z
			loc:          time.UTC,
			want:         "April 27, 2025, 2:30 PM UTC",
		},
		{
			name:         "fixed offset zone renders its own abbreviation and wall time",
			epochSeconds: 1745764200,
			loc:          time.FixedZone("CEST", 2*60*60),
			want:         "April 27, 2025, 4:30 PM CEST",
		},
		{
			name:         "epoch zero",
			epochSeconds: 0,
			loc:          time.UTC,
			want:         "January 1, 1970, 12:00 AM UTC",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := formatMessageTimestamp(tc.epochSeconds, tc.loc)
			if got != tc.want {
				t.Errorf("formatMessageTimestamp(%d) = %q, want %q", tc.epochSeconds, got, tc.want)
			}
		})
	}
}

func TestExpandMessageTimestampsIn(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{
			name: "valid token is replaced",
			body: "see you at <t:1745764200:F>",
			want: "see you at April 27, 2025, 2:30 PM UTC",
		},
		{
			name: "multiple tokens in one body are all replaced",
			body: "from <t:0:F> to <t:1745764200:F>",
			want: "from January 1, 1970, 12:00 AM UTC to April 27, 2025, 2:30 PM UTC",
		},
		{
			name: "adjacent tokens with no separator are both replaced",
			body: "<t:0:F><t:1745764200:F>",
			want: "January 1, 1970, 12:00 AM UTC" + "April 27, 2025, 2:30 PM UTC",
		},
		{
			name: "token at start of body",
			body: "<t:1745764200:F> hello",
			want: "April 27, 2025, 2:30 PM UTC hello",
		},
		{
			name: "token at end of body",
			body: "hello <t:1745764200:F>",
			want: "hello April 27, 2025, 2:30 PM UTC",
		},
		{
			name: ":R format is not supported in v1 and stays literal",
			body: "see you at <t:1745764200:R>",
			want: "see you at <t:1745764200:R>",
		},
		{
			name: "non-numeric value stays literal",
			body: "see you at <t:abc:F>",
			want: "see you at <t:abc:F>",
		},
		{
			name: "13+ digit value stays literal",
			body: "see you at <t:1234567890123:F>",
			want: "see you at <t:1234567890123:F>",
		},
		{
			name: "no token leaves body unchanged",
			body: "just a normal message",
			want: "just a normal message",
		},
		{
			name: "empty body stays empty",
			body: "",
			want: "",
		},
		{
			name: "token inside a fenced code block stays literal",
			body: "before\n```\n<t:1745764200:F>\n```\nafter",
			want: "before\n```\n<t:1745764200:F>\n```\nafter",
		},
		{
			name: "token inside inline code stays literal",
			body: "the token is `<t:1745764200:F>` in the body",
			want: "the token is `<t:1745764200:F>` in the body",
		},
		{
			name: "token inside a blockquote line stays literal",
			body: "> <t:1745764200:F> was announced",
			want: "> <t:1745764200:F> was announced",
		},
		{
			name: "token outside a code block on another line is still rendered",
			body: "```\nliteral <t:1745764200:F>\n```\nspoken <t:1745764200:F>",
			want: "```\nliteral <t:1745764200:F>\n```\nspoken April 27, 2025, 2:30 PM UTC",
		},
		{
			name: "token before inline code on the same line is still rendered",
			body: "<t:1745764200:F> then `<t:1745764200:F>`",
			want: "April 27, 2025, 2:30 PM UTC then `<t:1745764200:F>`",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := expandMessageTimestampsIn(tc.body, time.UTC)
			if got != tc.want {
				t.Errorf("expandMessageTimestampsIn(%q) = %q, want %q", tc.body, got, tc.want)
			}
		})
	}
}

func TestExpandMessageTimestampsUsesLocalZone(t *testing.T) {
	// expandMessageTimestamps is the production entry point and must use
	// time.Local rather than a fixed zone; check it actually renders (not a
	// literal token) without pinning an exact wall-clock string, since the
	// test host's local zone is unknown.
	got := expandMessageTimestamps("<t:1745764200:F>")
	if got == "<t:1745764200:F>" {
		t.Errorf("expandMessageTimestamps() left the token literal, want it rendered")
	}
}
