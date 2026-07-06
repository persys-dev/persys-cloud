package providers

import (
	"fmt"
	"path/filepath"
	"strings"
	"unicode"
)

func normalizeVolumeName(name string) (string, error) {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return "", fmt.Errorf("managed volume name is required")
	}

	var b strings.Builder
	for _, r := range trimmed {
		switch {
		case unicode.IsLetter(r), unicode.IsDigit(r):
			b.WriteRune(unicode.ToLower(r))
		case r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}

	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "", fmt.Errorf("managed volume name %q is invalid", name)
	}
	return out, nil
}

func volumeID(driver, name string) string {
	return strings.ToLower(strings.TrimSpace(driver)) + ":" + strings.TrimSpace(name)
}

func safeJoin(base, sub string) (string, error) {
	if strings.TrimSpace(base) == "" {
		return "", fmt.Errorf("base path is required")
	}
	cleanBase := filepath.Clean(base)
	path := filepath.Clean(filepath.Join(cleanBase, sub))
	if path == cleanBase {
		return path, nil
	}
	rel, err := filepath.Rel(cleanBase, path)
	if err != nil {
		return "", err
	}
	if strings.HasPrefix(rel, "..") {
		return "", fmt.Errorf("resolved path %q escapes base path", path)
	}
	return path, nil
}
