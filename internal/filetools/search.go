// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package filetools

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

const (
	defaultFindLimit = 100
	maxFindLimit     = 500
	defaultGrepLimit = 50
	maxGrepLimit     = 200
	maxGrepFileSize  = 1 << 20 // 1 MiB: skip larger files when grepping
	maxGrepLineLen   = 300     // clip long matching lines in the result
)

type searchArgs struct {
	Path    string `json:"path"`
	Pattern string `json:"pattern"`
	Limit   int    `json:"limit"`
}

func parseSearchArgs(arguments string) (searchArgs, error) {
	var a searchArgs
	if strings.TrimSpace(arguments) == "" {
		return a, nil
	}
	if err := json.Unmarshal([]byte(arguments), &a); err != nil {
		return a, fmt.Errorf("filetools: arguments: %w", err)
	}
	return a, nil
}

// searchFiles finds files whose name contains the pattern (case-insensitive),
// recursively under an optional sub-path.
func (t *Tool) searchFiles(arguments string) (string, error) {
	a, err := parseSearchArgs(arguments)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(a.Pattern) == "" {
		return "", fmt.Errorf("filetools: search_files needs a pattern")
	}
	limit := clampLimit(a.Limit, defaultFindLimit, maxFindLimit)
	start, err := resolveWithin(t.root, a.Path)
	if err != nil {
		return "", err
	}
	needle := strings.ToLower(a.Pattern)

	var hits []string
	truncated := false
	_ = walkProject(t.root, start, func(rel, abs string) error {
		if !strings.Contains(strings.ToLower(filepath.Base(rel)), needle) {
			return nil
		}
		if len(hits) >= limit {
			truncated = true
			return filepath.SkipAll
		}
		hits = append(hits, rel)
		return nil
	})

	header := fmt.Sprintf("Files matching %q under %s — %d", a.Pattern, displayPath(a.Path), len(hits))
	if truncated {
		header += " (limit reached; more exist — narrow the path or raise limit)"
	}
	if len(hits) == 0 {
		return header + ".", nil
	}
	return header + ":\n" + strings.Join(hits, "\n") + "\n", nil
}

// searchInFiles searches file contents for the pattern (case-insensitive), like
// grep, returning path:line: text. Binary and oversized files are skipped.
func (t *Tool) searchInFiles(arguments string) (string, error) {
	a, err := parseSearchArgs(arguments)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(a.Pattern) == "" {
		return "", fmt.Errorf("filetools: search_in_files needs a pattern")
	}
	limit := clampLimit(a.Limit, defaultGrepLimit, maxGrepLimit)
	start, err := resolveWithin(t.root, a.Path)
	if err != nil {
		return "", err
	}
	needle := strings.ToLower(a.Pattern)

	var matches []string
	truncated := false
	_ = walkProject(t.root, start, func(rel, abs string) error {
		if info, err := os.Stat(abs); err == nil && info.Size() > maxGrepFileSize {
			return nil
		}
		f, err := os.Open(abs)
		if err != nil {
			return nil
		}
		defer f.Close()
		head := make([]byte, 512)
		n, _ := f.Read(head)
		if isProbablyBinary(head[:n]) {
			return nil
		}
		if _, err := f.Seek(0, 0); err != nil {
			return nil
		}
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
		lineNo := 0
		for sc.Scan() {
			lineNo++
			line := sc.Text()
			if !strings.Contains(strings.ToLower(line), needle) {
				continue
			}
			if len(matches) >= limit {
				truncated = true
				return filepath.SkipAll
			}
			matches = append(matches, fmt.Sprintf("%s:%d: %s", rel, lineNo, clip(strings.TrimSpace(line), maxGrepLineLen)))
		}
		return nil
	})

	header := fmt.Sprintf("Matches for %q under %s — %d", a.Pattern, displayPath(a.Path), len(matches))
	if truncated {
		header += " (limit reached; more exist — narrow the path or raise limit)"
	}
	if len(matches) == 0 {
		return header + ".", nil
	}
	return header + ":\n" + strings.Join(matches, "\n") + "\n", nil
}

// walkProject walks the tree under startAbs, invoking fn for each regular file
// with its path relative to root (forward-slashed). It skips .git and
// node_modules directories and does not follow symlinks (WalkDir never does).
func walkProject(root, startAbs string, fn func(rel, abs string) error) error {
	return filepath.WalkDir(startAbs, func(abs string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // skip unreadable entries rather than aborting the whole walk
		}
		if d.IsDir() {
			if abs != startAbs && skipDirName(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil // skip symlinks, devices, sockets
		}
		rel, err := filepath.Rel(root, abs)
		if err != nil {
			return nil
		}
		return fn(filepath.ToSlash(rel), abs)
	})
}

func skipDirName(name string) bool {
	return name == ".git" || name == "node_modules"
}

func isProbablyBinary(data []byte) bool {
	return bytes.IndexByte(data, 0) >= 0
}

func clip(s string, max int) string {
	if r := []rune(s); len(r) > max {
		return string(r[:max]) + "…"
	}
	return s
}
