package cmd

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

// messageTimestampToken matches Chatto's FDR-030 inline timestamp token,
// <t:UNIX_SECONDS:F>. It's a pure message-body-text convention (no proto
// field), so any client that doesn't render it sees the raw token; this is
// the regex a conforming client uses to find and replace it. "F" is the
// only format v1 supports, and 1-12 digits is the only accepted width;
// anything else must be left untouched rather than replaced.
var messageTimestampToken = regexp.MustCompile(`<t:(\d{1,12}):F>`)

// messageTimestampFormat renders a token as an unambiguous absolute
// date-time, including the zone abbreviation. The terminal has no
// per-viewer "hover for details" popover the way the web app does, so the
// zone stays inline instead of being one interaction away.
const messageTimestampFormat = "January 2, 2006, 3:04 PM MST"

// expandMessageTimestamps replaces valid FDR-030 timestamp tokens in body
// with a localized date-time in the machine's local timezone, the
// terminal's analogue of the web app's viewer-specific timezone. See
// expandMessageTimestampsIn for the exclusion rules.
func expandMessageTimestamps(body string) string {
	return expandMessageTimestampsIn(body, time.Local)
}

// expandMessageTimestampsIn does the work for expandMessageTimestamps with
// an explicit zone, so tests don't depend on the host's local timezone.
//
// chatto-cli has no markdown parser, so unlike the reference web
// implementation (which walks a DOM and skips PRE/CODE/BLOCKQUOTE/A/BUTTON
// elements) the "stays literal in code and blockquotes" rule is applied by
// scanning the raw text: fenced blocks and blockquotes are tracked per
// line, inline code spans are tracked per backtick pair within a line.
// This covers the common cases (a token pasted into a code fence or a
// quoted announcement) without a full CommonMark parser.
func expandMessageTimestampsIn(body string, loc *time.Location) string {
	if !strings.Contains(body, "<t:") {
		return body
	}

	lines := strings.Split(body, "\n")
	inFence := false
	for i, line := range lines {
		trimmed := strings.TrimLeft(line, " \t")
		if strings.HasPrefix(trimmed, "```") {
			inFence = !inFence
			continue // the fence delimiter line itself is never a token
		}
		if inFence || strings.HasPrefix(trimmed, ">") {
			continue
		}
		lines[i] = expandTimestampsOutsideInlineCode(line, loc)
	}
	return strings.Join(lines, "\n")
}

// expandTimestampsOutsideInlineCode replaces tokens in line, leaving any
// backtick-delimited span untouched.
func expandTimestampsOutsideInlineCode(line string, loc *time.Location) string {
	var b strings.Builder
	rest := line
	for {
		open := strings.IndexByte(rest, '`')
		if open == -1 {
			b.WriteString(replaceTimestampTokens(rest, loc))
			break
		}
		b.WriteString(replaceTimestampTokens(rest[:open], loc))

		closeIdx := strings.IndexByte(rest[open+1:], '`')
		if closeIdx == -1 {
			// Unterminated span: the rest of the line renders as code
			// regardless, so leave it untouched too.
			b.WriteString(rest[open:])
			break
		}
		end := open + 1 + closeIdx + 1
		b.WriteString(rest[open:end]) // literal, backticks included
		rest = rest[end:]
	}
	return b.String()
}

// replaceTimestampTokens replaces every valid token in s. Invalid matches
// (rejected by parseMessageTimestampToken) are left as their original text.
func replaceTimestampTokens(s string, loc *time.Location) string {
	if !strings.Contains(s, "<t:") {
		return s
	}
	return messageTimestampToken.ReplaceAllStringFunc(s, func(match string) string {
		epochSeconds, ok := parseMessageTimestampToken(match)
		if !ok {
			return match
		}
		return formatMessageTimestamp(epochSeconds, loc)
	})
}

// parseMessageTimestampToken extracts the Unix-seconds value from a token
// already known to match messageTimestampToken. ok is false only if the
// digits overflow int64, which the regex's 12-digit cap makes unreachable
// in practice; it's kept as a defensive guard rather than a panic.
func parseMessageTimestampToken(token string) (epochSeconds int64, ok bool) {
	sub := messageTimestampToken.FindStringSubmatch(token)
	if sub == nil {
		return 0, false
	}
	epochSeconds, err := strconv.ParseInt(sub[1], 10, 64)
	if err != nil {
		return 0, false
	}
	return epochSeconds, true
}

// formatMessageTimestamp renders epochSeconds as an absolute date-time in loc.
func formatMessageTimestamp(epochSeconds int64, loc *time.Location) string {
	return time.Unix(epochSeconds, 0).In(loc).Format(messageTimestampFormat)
}
