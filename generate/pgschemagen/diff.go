// Copyright 2026 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package main

import (
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
)

type hunk struct {
	oldStart, newStart int
	oldLines, newLines []string
}

// applyDiff uses the PoC's exact line matching, but requires every hunk to have
// one unambiguous match and to change the source. Context-free insertions are
// rejected: their location cannot be verified against source contents.
func applyDiff(src, diff string) (string, error) {
	hunks, err := parseDiff(diff)
	if err != nil {
		return "", err
	}
	lines := splitLines(src)
	var out []string
	next := 0
	for n, h := range hunks {
		at := -1
		for i := next; i+len(h.oldLines) <= len(lines); i++ {
			if !slices.Equal(lines[i:i+len(h.oldLines)], h.oldLines) {
				continue
			}
			if at >= 0 {
				return "", fmt.Errorf("hunk %d is ambiguous", n+1)
			}
			at = i
		}
		if at < 0 {
			return "", fmt.Errorf("hunk %d (line %d) does not apply", n+1, h.oldStart)
		}
		out = append(out, lines[next:at]...)
		out = append(out, h.newLines...)
		next = at + len(h.oldLines)
	}
	out = append(out, lines[next:]...)
	result := strings.Join(out, "")
	if result == src {
		return "", fmt.Errorf("override makes no change")
	}
	return result, nil
}

func parseDiff(diff string) ([]hunk, error) {
	lines := splitLines(diff)
	i := 0
	if len(lines) > 0 && strings.HasPrefix(lines[0], "--- ") {
		if len(lines) < 2 || !strings.HasPrefix(lines[1], "+++ ") {
			return nil, fmt.Errorf("missing new-file diff header")
		}
		i = 2
	}
	var hunks []hunk
	oldEnd, newEnd := 0, 0
	for i < len(lines) {
		fields := strings.Fields(lines[i])
		if len(fields) < 4 || fields[0] != "@@" || fields[3] != "@@" ||
			!strings.HasPrefix(fields[1], "-") || !strings.HasPrefix(fields[2], "+") {
			return nil, fmt.Errorf("line %d: malformed hunk header", i+1)
		}
		oldStart, oldCount, err := parseRange(fields[1][1:])
		if err != nil {
			return nil, err
		}
		newStart, newCount, err := parseRange(fields[2][1:])
		if err != nil {
			return nil, err
		}
		if oldCount == 0 {
			return nil, fmt.Errorf("hunk has no source context")
		}
		if oldStart < oldEnd || newStart < newEnd {
			return nil, fmt.Errorf("overlapping or out-of-order hunks")
		}
		h := hunk{oldStart: oldStart, newStart: newStart}
		i++
		for i < len(lines) && (len(h.oldLines) < oldCount || len(h.newLines) < newCount) {
			line := lines[i]
			if len(line) == 0 || !strings.ContainsRune(" +-", rune(line[0])) {
				return nil, fmt.Errorf("line %d: unexpected diff line", i+1)
			}
			text := line[1:]
			i++
			if i < len(lines) && strings.TrimSuffix(lines[i], "\n") == `\ No newline at end of file` {
				text = strings.TrimSuffix(text, "\n")
				i++
			}
			if line[0] != '+' {
				h.oldLines = append(h.oldLines, text)
			}
			if line[0] != '-' {
				h.newLines = append(h.newLines, text)
			}
		}
		if len(h.oldLines) != oldCount || len(h.newLines) != newCount {
			return nil, fmt.Errorf("hunk line counts do not match header")
		}
		if slices.Equal(h.oldLines, h.newLines) {
			return nil, fmt.Errorf("hunk makes no change")
		}
		hunks = append(hunks, h)
		oldEnd, newEnd = oldStart+oldCount, newStart+newCount
	}
	if len(hunks) == 0 {
		return nil, fmt.Errorf("override contains no hunks")
	}
	return hunks, nil
}

func parseRange(s string) (int, int, error) {
	start, count, hasCount := strings.Cut(s, ",")
	n, err := strconv.Atoi(start)
	if err != nil || n < 0 {
		return 0, 0, fmt.Errorf("invalid diff range %q", s)
	}
	size := 1
	if hasCount {
		size, err = strconv.Atoi(count)
	}
	if err != nil || size < 0 || n == 0 && size != 0 || n > math.MaxInt-size {
		return 0, 0, fmt.Errorf("invalid diff range %q", s)
	}
	return n, size, nil
}

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
