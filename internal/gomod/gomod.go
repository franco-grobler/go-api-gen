// Package gomod derives Go import paths from directory paths.
//
// Generated code in one package imports generated code in another, so the
// generator has to know the import path of every output directory. Walking up
// to the nearest go.mod works for a single-module monorepo and for a
// multi-module one under go.work alike: each output directory resolves against
// whichever module actually encloses it.
package gomod

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Module is a Go module found on disk.
type Module struct {
	Path string // module path from the go.mod directive
	Dir  string // absolute directory containing go.mod
}

// Resolver caches go.mod lookups across a generation run.
type Resolver struct {
	cache map[string]*Module // directory -> enclosing module
}

// NewResolver returns an empty Resolver.
func NewResolver() *Resolver {
	return &Resolver{cache: map[string]*Module{}}
}

// Find returns the module enclosing dir. The directory need not exist yet,
// which matters because output directories are resolved before they are
// created.
func (r *Resolver) Find(dir string) (*Module, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	if m, ok := r.cache[abs]; ok {
		if m == nil {
			return nil, fmt.Errorf(
				"no go.mod found in %s or any parent directory",
				dir,
			)
		}
		return m, nil
	}

	for cur := abs; ; {
		gomod := filepath.Join(cur, "go.mod")
		if data, err := os.ReadFile(gomod); err == nil {
			path, err := modulePath(data)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", gomod, err)
			}
			m := &Module{Path: path, Dir: cur}
			r.cache[abs] = m
			return m, nil
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			r.cache[abs] = nil
			return nil, fmt.Errorf(
				"no go.mod found in %s or any parent directory",
				dir,
			)
		}
		cur = parent
	}
}

// ImportPath returns the Go import path of dir.
func (r *Resolver) ImportPath(dir string) (string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	m, err := r.Find(abs)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(m.Dir, abs)
	if err != nil {
		return "", err
	}
	if rel == "." {
		return m.Path, nil
	}
	if strings.HasPrefix(rel, "..") {
		return "", fmt.Errorf("%s is outside module %s", dir, m.Path)
	}
	return m.Path + "/" + filepath.ToSlash(rel), nil
}

// modulePath extracts the module path from go.mod content, handling both the
// bare and quoted forms as well as a module directive inside a factored block.
func modulePath(data []byte) (string, error) {
	sc := bufio.NewScanner(strings.NewReader(string(data)))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if i := strings.Index(line, "//"); i >= 0 {
			line = strings.TrimSpace(line[:i])
		}
		rest, ok := strings.CutPrefix(line, "module")
		if !ok || (rest != "" && !isSpace(rest[0])) {
			continue
		}
		rest = strings.TrimSpace(rest)
		if rest == "(" || rest == "" {
			continue // factored block; the path is on a following line
		}
		if unquoted, err := strconv.Unquote(rest); err == nil {
			return unquoted, nil
		}
		return strings.Fields(rest)[0], nil
	}
	if err := sc.Err(); err != nil {
		return "", err
	}
	return "", fmt.Errorf("no module directive found")
}

func isSpace(b byte) bool { return b == ' ' || b == '\t' }
