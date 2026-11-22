//go:build !windows

package clientruntime

import (
	"os"
	"path/filepath"
	"strings"
)

const pathBlockStart = "# jieshangai-path-start"
const pathBlockEnd = "# jieshangai-path-end"

func pathSeparator() rune { return os.PathSeparator }

func ensureUserPath(dir string) error {
	home := homeDir()
	block := pathBlockStart + "\nexport PATH=" + shellQuote(dir) + ":$PATH\n" + pathBlockEnd
	for _, name := range []string{".zprofile", ".bash_profile"} {
		path := filepath.Join(home, name)
		data, err := os.ReadFile(path)
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		text := strings.ReplaceAll(string(data), "\r\n", "\n")
		if strings.Contains(text, block) {
			continue
		}
		// An older block may point at a different directory; replace it.
		text = stripPathBlock(text)
		if text != "" && !strings.HasSuffix(text, "\n") {
			text += "\n"
		}
		mode := os.FileMode(0600)
		if info, statErr := os.Stat(path); statErr == nil {
			mode = info.Mode().Perm()
		}
		if err := os.WriteFile(path, []byte(text+block+"\n"), mode); err != nil {
			return err
		}
	}
	return nil
}

func removeUserPath(dir string) error {
	for _, name := range []string{".zprofile", ".bash_profile"} {
		path := filepath.Join(homeDir(), name)
		data, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
		out := lines[:0]
		skip := false
		for _, line := range lines {
			if line == pathBlockStart {
				skip = true
				continue
			}
			if line == pathBlockEnd {
				skip = false
				continue
			}
			if !skip {
				out = append(out, line)
			}
		}
		mode := os.FileMode(0600)
		if info, statErr := os.Stat(path); statErr == nil {
			mode = info.Mode().Perm()
		}
		if err := os.WriteFile(path, []byte(strings.Join(out, "\n")), mode); err != nil {
			return err
		}
	}
	return nil
}

func stripPathBlock(text string) string {
	lines := strings.Split(text, "\n")
	out := lines[:0]
	skip := false
	for _, line := range lines {
		if line == pathBlockStart {
			skip = true
			continue
		}
		if line == pathBlockEnd {
			skip = false
			continue
		}
		if !skip {
			out = append(out, line)
		}
	}
	return strings.Join(out, "\n")
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'" }
