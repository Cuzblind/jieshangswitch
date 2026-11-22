package clientruntime

import "path/filepath"

// toolBinDir is where writeLauncher puts the claude/codex shims.
func toolBinDir() string { return filepath.Join(toolRoot(), "bin") }

// legacyToolPathEntry is what an earlier build wrote by mistake: the tools
// root itself, which holds no launchers. Compare it without any separator:
// on Windows the old build stored "<root>;", and splitting PATH on ";"
// leaves "<root>".
func legacyToolPathEntry() string { return toolRoot() }

// ensurePersistentToolPath makes the managed launcher directory available to
// newly started terminals after a verified CLI install. It is deliberately
// separate from augmentedPath, which only affects child processes. A legacy
// entry from the earlier build is removed first so it gets corrected.
func ensurePersistentToolPath() error {
	if err := removeUserPath(legacyToolPathEntry()); err != nil {
		return err
	}
	return ensureUserPath(toolBinDir())
}

func removePersistentToolPath() error {
	if err := removeUserPath(toolBinDir()); err != nil {
		return err
	}
	return removeUserPath(legacyToolPathEntry())
}
