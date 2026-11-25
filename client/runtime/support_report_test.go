package clientruntime

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type countedDiagnosticReader struct {
	io.ReaderAt
	bytes int
}

func (r *countedDiagnosticReader) ReadAt(p []byte, offset int64) (int, error) {
	n, err := r.ReaderAt.ReadAt(p, offset)
	r.bytes += n
	return n, err
}

func TestDiagnosticTailReadsBoundedCompleteRecords(t *testing.T) {
	line := `{"output":"下载完成"}` + "\n"
	for _, text := range []string{"", line, strings.Repeat(line, 100000), strings.Repeat("x", 20000)} {
		r := &countedDiagnosticReader{ReaderAt: strings.NewReader(text)}
		got := readDiagnosticTail(r, int64(len(text)))
		if r.bytes > 16000 {
			t.Fatalf("read %d bytes to publish a 16000-byte tail", r.bytes)
		}
		if len(text) <= 16000 && string(got) != text {
			t.Fatalf("small log changed: %q", got)
		}
		if strings.HasSuffix(text, line) && !strings.HasSuffix(string(got), line) {
			t.Fatal("recent diagnostic record was lost")
		}
		for _, row := range strings.Split(strings.TrimSpace(string(got)), "\n") {
			if row != "" && !json.Valid([]byte(row)) {
				t.Fatalf("published a partial record: %q", row)
			}
		}
	}
}

func BenchmarkSupportReportWithDiagnostics(b *testing.B) {
	root := b.TempDir()
	dir := filepath.Join(root, "diagnostics")
	if err := os.MkdirAll(dir, 0700); err != nil {
		b.Fatal(err)
	}
	line := `{"at":"2026-11-24T01:00:00Z","agent":"claude","stage":"download","output":"https://example.test/file?token=private-value"}` + "\n"
	for _, agent := range []string{"claude", "codex"} {
		if err := os.WriteFile(filepath.Join(dir, agent+".jsonl"), []byte(strings.Repeat(line, (1<<20)/len(line))), 0600); err != nil {
			b.Fatal(err)
		}
	}
	m := &toolManager{root: root, states: map[string]ToolStatus{}}
	a := NewDesktop(Env{InstallDiagnostics: m.diagnostics}, "benchmark")
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		_ = a.supportReport()
	}
}

func TestSupportDiagnosticsDoNotHoldStateLock(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	a := NewDesktop(Env{InstallDiagnostics: func(agent string) string {
		if agent == "claude" {
			close(started)
			<-release
		}
		return agent + " diagnostics"
	}}, "test-version")
	report := make(chan string, 1)
	go func() { report <- a.supportReport() }()
	<-started
	changed := make(chan struct{})
	go func() { a.setView(View{Screen: "home", Notice: "cancelled"}); close(changed) }()
	defer func() {
		close(release)
		if text := <-report; !strings.Contains(text, "claude diagnostics\ncodex diagnostics") || !strings.Contains(text, "test-version") {
			t.Errorf("incomplete support report: %s", text)
		}
		<-changed
	}()
	select {
	case <-changed:
	case <-time.After(time.Second):
		t.Fatal("slow diagnostic I/O blocked state changes")
	}
}
