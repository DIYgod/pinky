package main

import (
	"errors"
	"fmt"
	"strings"
)

// CHANGELOG.md is a short introduction, then a section per release, newest
// first:
//
//	## 2026.1107.2 (2026-11-07)
//
//	- Short, user-facing notes.
//
// A section starts at a line "## " (a level-two heading) whose first word
// is the version; the rest of the heading is the date in parentheses. Notes
// for the next release wait under "## Unreleased" at the top, which `tag`
// turns into the release's heading. Headings inside fenced code blocks do
// not count.

// unreleased is the heading word of the notes not released yet.
const unreleased = "Unreleased"

// section is one release's part of the changelog.
type section struct {
	version string // the heading's first word: a version or "Unreleased"
	line    int    // the index of the heading line
	notes   string // the Markdown under the heading, trimmed
}

func (s section) isUnreleased() bool { return strings.EqualFold(s.version, unreleased) }

// splitLines splits text into lines without their line breaks (and a "\r"
// of Windows line breaks).
func splitLines(text string) []string {
	lines := strings.Split(text, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimSuffix(l, "\r")
	}
	return lines
}

// sections returns the changelog's sections, in the file's order.
func sections(text string) []section {
	lines := splitLines(text)
	var out []section
	fence := ""
	start := -1
	flush := func(end int) {
		if start >= 0 {
			out[len(out)-1].notes = strings.TrimSpace(strings.Join(lines[start+1:end], "\n"))
		}
	}
	for i, l := range lines {
		trimmed := strings.TrimSpace(l)
		if fence != "" {
			if strings.HasPrefix(trimmed, fence) {
				fence = ""
			}
			continue
		}
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			fence = trimmed[:3]
			continue
		}
		rest, ok := strings.CutPrefix(l, "## ")
		if !ok {
			continue
		}
		flush(i)
		fields := strings.Fields(rest)
		v := ""
		if len(fields) > 0 {
			v = strings.TrimPrefix(fields[0], "v")
		}
		out = append(out, section{version: v, line: i})
		start = i
	}
	flush(len(lines))
	return out
}

// Notes returns the notes of version in the changelog.
func Notes(text, version string) (string, error) {
	version = strings.TrimPrefix(version, "v")
	for _, s := range sections(text) {
		if s.version == version || strings.EqualFold(version, unreleased) && s.isUnreleased() {
			if s.notes == "" {
				return "", fmt.Errorf("CHANGELOG.md has no notes under its %s heading", s.version)
			}
			return s.notes, nil
		}
	}
	return "", fmt.Errorf("CHANGELOG.md has no section for %s (a \"## %s (%s)\" heading)", version, version, dateOf(version))
}

// dateOf returns the date of a release version, or YYYY-MM-DD.
func dateOf(version string) string {
	if v, err := ParseVersion(version); err == nil {
		return v.Date()
	}
	return "YYYY-MM-DD"
}

// errNoRelease is the error of a changelog without notes to release.
var errNoRelease = errors.New(`CHANGELOG.md has nothing to release: write this release's notes under a "## Unreleased" heading at its top`)

// Stamp returns the changelog with the notes of the next release under the
// heading of version v: the first section's, when it is "Unreleased" or a
// version that has no tag yet (tagged reports which have one), which
// happens when a release was prepared but its tag not pushed, or another
// release took its version first. It also returns that section's notes.
func Stamp(text string, v Version, tagged func(version string) bool) (string, string, error) {
	all := sections(text)
	if len(all) == 0 {
		return "", "", errNoRelease
	}
	top := all[0]
	if !top.isUnreleased() {
		if _, err := ParseVersion(top.version); err != nil {
			return "", "", fmt.Errorf("CHANGELOG.md's first section is %q, neither \"Unreleased\" nor a release version", top.version)
		}
		if tagged(top.version) {
			return "", "", errNoRelease
		}
	}
	if top.notes == "" {
		return "", "", fmt.Errorf("CHANGELOG.md has no notes under its %s heading: write what this release changes for people", top.version)
	}
	for _, s := range all[1:] {
		if s.isUnreleased() {
			return "", "", errors.New(`CHANGELOG.md has an "Unreleased" section below a release: notes for the next release go at the top`)
		}
		if s.version == v.String() {
			return "", "", fmt.Errorf("CHANGELOG.md already has a section for %s", v)
		}
	}
	lines := strings.Split(text, "\n")
	heading := "## " + v.String() + " (" + v.Date() + ")"
	if strings.HasSuffix(lines[top.line], "\r") {
		heading += "\r"
	}
	lines[top.line] = heading
	return strings.Join(lines, "\n"), top.notes, nil
}
