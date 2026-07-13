package permission

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type PathValidator struct {
	workspace string
	protected []string
}

func NewPathValidator(workspace string, protectedPaths []string) (*PathValidator, error) {
	root, err := canonicalExistingPath(workspace)
	if err != nil {
		return nil, fmt.Errorf("resolve workspace: %w", err)
	}
	v := &PathValidator{workspace: root}
	for _, path := range protectedPaths {
		expanded, err := expandPath(path)
		if err != nil {
			return nil, err
		}
		expanded = platformAbsolute(expanded, root)
		if !filepath.IsAbs(expanded) {
			expanded = filepath.Join(root, expanded)
		}
		resolved, err := canonicalPath(expanded)
		if err != nil {
			return nil, fmt.Errorf("resolve protected path %q: %w", path, err)
		}
		v.protected = append(v.protected, resolved)
	}
	return v, nil
}

func (v *PathValidator) Workspace() string { return v.workspace }

// Validate resolves path (including existing symlinks) and ensures it remains in the workspace.
func (v *PathValidator) Validate(path, workingDirectory string) (string, error) {
	if v == nil || v.workspace == "" {
		return "", errors.New("path validator is not configured")
	}
	if strings.TrimSpace(path) == "" {
		return "", errors.New("path is required")
	}
	expanded, err := expandPath(path)
	if err != nil {
		return "", err
	}
	expanded = platformAbsolute(expanded, v.workspace)
	if !filepath.IsAbs(expanded) {
		base := workingDirectory
		if strings.TrimSpace(base) == "" {
			base = v.workspace
		} else if !filepath.IsAbs(base) {
			base = filepath.Join(v.workspace, base)
		}
		expanded = filepath.Join(base, expanded)
	}
	resolved, err := canonicalPath(expanded)
	if err != nil {
		return "", err
	}
	if !isWithin(v.workspace, resolved) {
		return "", fmt.Errorf("path %q escapes workspace %q", resolved, v.workspace)
	}
	for _, denied := range v.protected {
		// A filesystem root protects the root object itself. Treating it as a
		// subtree would also deny every legitimate workspace on that volume.
		isVolumeRoot := filepath.Dir(denied) == denied
		if (isVolumeRoot && samePath(denied, resolved)) || (!isVolumeRoot && isWithin(denied, resolved)) {
			return "", fmt.Errorf("path %q is protected", resolved)
		}
	}
	return resolved, nil
}

// platformAbsolute maps policy paths written in the documented POSIX form
// (for example /etc) onto the current volume when running on Windows.
func platformAbsolute(path, base string) string {
	if filepath.IsAbs(path) {
		return path
	}
	if strings.HasPrefix(path, "/") {
		if volume := filepath.VolumeName(base); volume != "" {
			return volume + filepath.FromSlash(path)
		}
	}
	return path
}

func samePath(a, b string) bool {
	return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
}

func canonicalExistingPath(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(filepath.Clean(abs))
}

// canonicalPath also supports a not-yet-created leaf by resolving its nearest existing parent.
func canonicalPath(path string) (string, error) {
	abs, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return "", err
	}
	cursor := abs
	var suffix []string
	for {
		resolved, evalErr := filepath.EvalSymlinks(cursor)
		if evalErr == nil {
			for i := len(suffix) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, suffix[i])
			}
			return filepath.Clean(resolved), nil
		}
		if !os.IsNotExist(evalErr) {
			return "", evalErr
		}
		parent := filepath.Dir(cursor)
		if parent == cursor {
			return "", evalErr
		}
		suffix = append(suffix, filepath.Base(cursor))
		cursor = parent
	}
}

func expandPath(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "~" || strings.HasPrefix(path, "~/") || strings.HasPrefix(path, `~\`) {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve home directory: %w", err)
		}
		if path == "~" {
			return home, nil
		}
		return filepath.Join(home, path[2:]), nil
	}
	return path, nil
}

func isWithin(root, target string) bool {
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}
