package clientruntime

import "testing"

// Latest versions only came from an in-memory check, so after a restart the
// update button disappeared until the next daily check.
func TestLatestVersionsSurviveRestart(t *testing.T) {
	home := t.TempDir()
	t.Setenv("RELAY_HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOME", home)
	m := newToolManager()
	m.recordLatest("codex", "0.9.0", "26.1101.0.0")

	next := newToolManager()
	s := next.state("codex")
	if s.LatestCLI != "0.9.0" || s.LatestGUI != "26.1101.0.0" {
		t.Fatalf("latest versions lost on restart: %+v", s)
	}
	// An empty result (source unreachable) must not erase what is known.
	next.recordLatest("codex", "", "")
	if s := newToolManager().state("codex"); s.LatestCLI != "0.9.0" || s.LatestGUI != "26.1101.0.0" {
		t.Fatalf("failed check erased known versions: %+v", s)
	}
}
