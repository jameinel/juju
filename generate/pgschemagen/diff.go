// Copyright 2026 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package main

import (
	"fmt"
	"strconv"
	"strings"
)

// hunk is one hunk of a unified diff.
type hunk struct {
	oldStart int
	oldCount int
	newCount int
	oldLines []string
	newLines []string
}

// applyDiff applies a unified diff to src. Each hunk must match its context
// exactly, but may be found at an offset from its stated line. An empty diff
// leaves src unchanged.
func applyDiff(src, diff string) (string, error) {
	hunks, err := parseDiff(diff)
	if err != nil {
		return "", err
	}
	lines := splitLines(src)
	var (
		out  []string
		next int
	)
	for n, h := range hunks {
		at, ok := findHunk(lines, h, next)
		if !ok {
			return "", fmt.Errorf("hunk %d (line %d) does not apply", n+1, h.oldStart)
		}
		out = append(out, lines[next:at]...)
		out = append(out, h.newLines...)
		next = at + len(h.oldLines)
	}
	out = append(out, lines[next:]...)
	return strings.Join(out, ""), nil
}

// findHunk returns the line at which the hunk's old lines match, searching
// outwards from its stated position but not before from.
func findHunk(lines []string, h hunk, from int) (int, bool) {
	want := h.oldStart - 1
	if h.oldCount == 0 {
		want = h.oldStart
	}
	want = max(want, from)
	for offset := 0; ; offset++ {
		lo, hi := want-offset, want+offset
		if lo < from && hi+len(h.oldLines) > len(lines) {
			return 0, false
		}
		if lo >= from && matchAt(lines, h.oldLines, lo) {
			return lo, true
		}
		if offset > 0 && matchAt(lines, h.oldLines, hi) {
			return hi, true
		}
	}
}

func matchAt(lines, want []string, at int) bool {
	if at < 0 || at+len(want) > len(lines) {
		return false
	}
	for i, l := range want {
		if lines[at+i] != l {
			return false
		}
	}
	return true
}

// parseDiff parses the hunks of a unified diff, ignoring file headers.
func parseDiff(diff string) ([]hunk, error) {
	var (
		hunks []hunk
		cur   *hunk
	)
	lines := splitLines(diff)
	for i := 0; i < len(lines); i++ {
		l := lines[i]
		noEOL := i+1 < len(lines) && strings.HasPrefix(lines[i+1], `\ `)
		if noEOL {
			i++
		}
		switch {
		case strings.HasPrefix(l, "@@"):
			header, err := parseHunkHeader(l)
			if err != nil {
				return nil, err
			}
			hunks = append(hunks, header)
			cur = &hunks[len(hunks)-1]
		case cur == nil:
			// File headers and any preamble before the first hunk.
		case strings.HasPrefix(l, " "), l == "\n":
			text := trimMarker(l, noEOL)
			cur.oldLines = append(cur.oldLines, text)
			cur.newLines = append(cur.newLines, text)
		case strings.HasPrefix(l, "-"):
			cur.oldLines = append(cur.oldLines, trimMarker(l, noEOL))
		case strings.HasPrefix(l, "+"):
			cur.newLines = append(cur.newLines, trimMarker(l, noEOL))
		default:
			return nil, fmt.Errorf("line %d: unexpected diff line %q", i+1, strings.TrimSpace(l))
		}
	}
	if len(hunks) == 0 && strings.TrimSpace(diff) != "" {
		return nil, fmt.Errorf("non-empty diff has no hunks")
	}
	for i, h := range hunks {
		if len(h.oldLines) != h.oldCount || len(h.newLines) != h.newCount {
			return nil, fmt.Errorf("hunk %d: line counts do not match header", i+1)
		}
	}
	return hunks, nil
}

func trimMarker(l string, noEOL bool) string {
	if l == "\n" {
		return l
	}
	l = l[1:]
	if noEOL {
		l = strings.TrimSuffix(l, "\n")
	}
	return l
}

func parseHunkHeader(l string) (hunk, error) {
	fields := strings.Fields(l)
	if len(fields) < 4 || fields[0] != "@@" || fields[3] != "@@" ||
		!strings.HasPrefix(fields[1], "-") || !strings.HasPrefix(fields[2], "+") {
		return hunk{}, fmt.Errorf("malformed hunk header %q", l)
	}
	start, oldCount, err := parseRange(fields[1][1:])
	if err != nil {
		return hunk{}, fmt.Errorf("malformed hunk header %q: %w", l, err)
	}
	_, newCount, err := parseRange(fields[2][1:])
	if err != nil {
		return hunk{}, fmt.Errorf("malformed hunk header %q: %w", l, err)
	}
	return hunk{oldStart: start, oldCount: oldCount, newCount: newCount}, nil
}

func parseRange(s string) (int, int, error) {
	start, count, hasCount := strings.Cut(s, ",")
	n, err := strconv.Atoi(start)
	if err != nil || n < 0 {
		return 0, 0, fmt.Errorf("invalid range %q", s)
	}
	size := 1
	if hasCount {
		size, err = strconv.Atoi(count)
	}
	if err != nil || size < 0 || (n == 0 && size > 0) {
		return 0, 0, fmt.Errorf("invalid range %q", s)
	}
	return n, size, nil
}

// splitLines splits text into lines, keeping their line endings.
func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	lines := strings.SplitAfter(s, "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}
