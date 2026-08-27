package main

import (
	"bufio"
	"fmt"
	"io"
	"path"
	"strconv"
	"strings"
)

// Block identifies one counted region of one file in a Go coverage profile.
//
// Position is part of the identity because `go test -coverpkg ./...` emits the
// same block once per test binary that loaded the package, so the raw profile
// contains duplicates. Keying on position lets those collapse.
type Block struct {
	File     string
	StartPos string
	EndPos   string
	Stmts    int
}

// Tally is a statement count and how many of those statements were reached.
type Tally struct {
	Covered int
	Total   int
}

// Percent is the share of statements reached, or 100 for a package that has no
// statements at all: there is nothing there to leave untested.
func (t Tally) Percent() float64 {
	if t.Total == 0 {
		return 100
	}
	return float64(t.Covered) / float64(t.Total) * 100
}

// ParseProfile reads a Go coverage profile and returns each distinct block with
// whether any test reached it. Duplicate blocks are ORed rather than summed:
// the question is coverage, not hit count.
func ParseProfile(r io.Reader) (map[Block]bool, error) {
	blocks := make(map[Block]bool)
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for line := 1; sc.Scan(); line++ {
		text := strings.TrimSpace(sc.Text())
		if text == "" || strings.HasPrefix(text, "mode:") {
			continue
		}
		block, hit, err := parseBlock(text)
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", line, err)
		}
		blocks[block] = blocks[block] || hit
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return blocks, nil
}

// parseBlock decodes one profile line: "file.go:1.2,3.4 5 6": positions,
// statement count, hit count.
func parseBlock(text string) (Block, bool, error) {
	fields := strings.Fields(text)
	if len(fields) != 3 {
		return Block{}, false, fmt.Errorf("want 3 fields, got %d: %q", len(fields), text)
	}
	colon := strings.LastIndex(fields[0], ":")
	comma := strings.LastIndex(fields[0], ",")
	if colon < 0 || comma < colon {
		return Block{}, false, fmt.Errorf("malformed position %q", fields[0])
	}
	stmts, err := strconv.Atoi(fields[1])
	if err != nil {
		return Block{}, false, fmt.Errorf("statement count %q: %w", fields[1], err)
	}
	count, err := strconv.Atoi(fields[2])
	if err != nil {
		return Block{}, false, fmt.Errorf("hit count %q: %w", fields[2], err)
	}
	return Block{
		File:     fields[0][:colon],
		StartPos: fields[0][colon+1 : comma],
		EndPos:   fields[0][comma+1:],
		Stmts:    stmts,
	}, count > 0, nil
}

// Tallies groups blocks into per-package statement counts, keyed by the package
// path relative to modulePath ("internal/proxy"). A file outside the module is
// skipped rather than reported under a misleading key.
func Tallies(blocks map[Block]bool, modulePath string) map[string]Tally {
	prefix := strings.TrimSuffix(modulePath, "/") + "/"
	out := make(map[string]Tally)
	for block, hit := range blocks {
		if !strings.HasPrefix(block.File, prefix) {
			continue
		}
		pkg := path.Dir(strings.TrimPrefix(block.File, prefix))
		t := out[pkg]
		t.Total += block.Stmts
		if hit {
			t.Covered += block.Stmts
		}
		out[pkg] = t
	}
	return out
}
