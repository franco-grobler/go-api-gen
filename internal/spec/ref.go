package spec

import (
	"fmt"
	"net/url"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Location identifies a node in the workspace: an absolute file path plus a
// JSON pointer into that file. It is the canonical identity of every named
// component, and is what lets a schema authored once be recognised as the same
// schema no matter how many service specs reference it.
type Location struct {
	File    string // absolute path
	Pointer string // RFC 6901 JSON pointer, e.g. /components/schemas/User
}

func (l Location) String() string { return l.File + "#" + l.Pointer }

// IsZero reports whether the location is unset, which is the case for schemas
// written inline rather than as a named component.
func (l Location) IsZero() bool { return l.File == "" }

// ComponentName returns the trailing segment of the pointer, which for a
// component is its name under components/schemas.
func (l Location) ComponentName() string {
	i := strings.LastIndex(l.Pointer, "/")
	if i < 0 {
		return l.Pointer
	}
	return unescapePointerToken(l.Pointer[i+1:])
}

// parseRef resolves a $ref string that appeared in file into a Location.
// Relative file references resolve against the referring file's directory,
// which is what makes ../../shared/user.yaml#/components/schemas/User work
// from anywhere in the tree.
func parseRef(file, ref string) (Location, error) {
	if ref == "" {
		return Location{}, fmt.Errorf("$ref is empty")
	}
	if strings.Contains(ref, "://") {
		return Location{}, fmt.Errorf("$ref %q: remote references are not "+
			"supported; vendor the document into the workspace instead", ref)
	}

	target, pointer, _ := strings.Cut(ref, "#")
	if pointer != "" && !strings.HasPrefix(pointer, "/") {
		return Location{}, fmt.Errorf(
			"$ref %q: fragment must be a JSON pointer beginning with /",
			ref,
		)
	}
	// A ref may percent-encode its path portion.
	if dec, err := url.PathUnescape(target); err == nil {
		target = dec
	}

	abs := file
	if target != "" {
		if filepath.IsAbs(target) {
			abs = filepath.Clean(target)
		} else {
			abs = filepath.Clean(filepath.Join(filepath.Dir(file), target))
		}
	}
	return Location{File: abs, Pointer: pointer}, nil
}

// resolvePointer walks a JSON pointer through a YAML document.
func resolvePointer(root *yaml.Node, pointer string) (*yaml.Node, error) {
	n := unwrap(root)
	if n != nil && n.Kind == yaml.DocumentNode && len(n.Content) > 0 {
		n = unwrap(n.Content[0])
	}
	if pointer == "" {
		return n, nil
	}
	for _, raw := range strings.Split(strings.TrimPrefix(pointer, "/"), "/") {
		token := unescapePointerToken(raw)
		if n == nil {
			return nil, fmt.Errorf("pointer %q: no such node", pointer)
		}
		switch n.Kind {
		case yaml.MappingNode:
			next := mapGet(n, token)
			if next == nil {
				return nil, fmt.Errorf(
					"pointer %q: key %q not found",
					pointer,
					token,
				)
			}
			n = next
		case yaml.SequenceNode:
			var idx int
			if _, err := fmt.Sscanf(
				token,
				"%d",
				&idx,
			); err != nil || idx < 0 || idx >= len(n.Content) {
				return nil, fmt.Errorf(
					"pointer %q: %q is not a valid index into a "+
						"%d-element sequence",
					pointer, token, len(n.Content))
			}
			n = unwrap(n.Content[idx])
		default:
			return nil, fmt.Errorf(
				"pointer %q: cannot descend into %s at %q",
				pointer,
				kindName(n),
				token,
			)
		}
	}
	return n, nil
}

func unescapePointerToken(s string) string {
	// Order matters: ~1 must be decoded before ~0, per RFC 6901.
	return strings.ReplaceAll(strings.ReplaceAll(s, "~1", "/"), "~0", "~")
}

func escapePointerToken(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "~", "~0"), "/", "~1")
}
