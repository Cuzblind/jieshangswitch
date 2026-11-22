package clientruntime

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const (
	codexPublisher  = "CN=50BDFD77-8903-4850-9FFE-6E8522F64D5B"
	claudePublisher = `CN="Anthropic, PBC", O="Anthropic, PBC", L=San Francisco, S=California, C=US, SERIALNUMBER=4860621, OID.2.5.4.15=Private Organization, OID.1.3.6.1.4.1.311.60.2.1.2=Delaware, OID.1.3.6.1.4.1.311.60.2.1.3=US`
)

// A package shaped like the real Codex MSIX: thousands of entries, so reading
// the central directory piecemeal would trip the mirror's 429 rate limit.
func remoteMSIX(t *testing.T, name, publisher, version string) []byte {
	t.Helper()
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	f, err := z.Create("AppxManifest.xml")
	if err != nil {
		t.Fatal(err)
	}
	escaped := strings.ReplaceAll(publisher, `"`, "&quot;")
	_, _ = fmt.Fprintf(f, `<Package><Identity Name="%s" Publisher="%s" Version="%s" ProcessorArchitecture="x64" /></Package>`, name, escaped, version)
	for i := 0; i < 3700; i++ {
		entry, err := z.Create(fmt.Sprintf("app/resources/file-%05d.bin", i))
		if err != nil {
			t.Fatal(err)
		}
		_, _ = entry.Write([]byte("x"))
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func rangeServer(t *testing.T, body []byte, requests *atomic.Int32) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests != nil {
			requests.Add(1)
		}
		http.ServeContent(w, r, "app.msix", time.Time{}, bytes.NewReader(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func testDownloader(srv *httptest.Server) *toolDownloader {
	return &toolDownloader{client: srv.Client(), allow: func(string) bool { return true }}
}

// Windows GUI sources carry no version, so LatestGUI stayed empty and desktop
// updates were neither offered nor installed.
func TestRemoteMSIXVersionReadsManifestWithFewRequests(t *testing.T) {
	var requests atomic.Int32
	srv := rangeServer(t, remoteMSIX(t, "OpenAI.Codex", codexPublisher, "26.1027.3384.0"), &requests)
	got, err := testDownloader(srv).remoteMSIXVersion(context.Background(), srv.URL, "codex", "amd64")
	if err != nil || got != "26.1027.3384.0" {
		t.Fatalf("version %q err %v", got, err)
	}
	if n := requests.Load(); n > 6 {
		t.Fatalf("used %d requests; a rate-limited mirror answers 429", n)
	}
}

func TestRemoteMSIXVersionRejectsForeignPackage(t *testing.T) {
	srv := rangeServer(t, remoteMSIX(t, "OpenAI.Codex", "CN=OpenAI Lookalike", "99.0.0.0"), nil)
	if v, err := testDownloader(srv).remoteMSIXVersion(context.Background(), srv.URL, "codex", "amd64"); err == nil {
		t.Fatalf("foreign package offered as update %q", v)
	}
}

func TestRemoteMSIXVersionFallsBackToNextSource(t *testing.T) {
	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer down.Close()
	good := rangeServer(t, remoteMSIX(t, "Claude", claudePublisher, "4.29.0.0"), nil)
	got, err := testDownloader(good).firstRemoteMSIXVersion(context.Background(), []string{down.URL, good.URL}, "claude", "amd64")
	if err != nil || got != "4.29.0.0" {
		t.Fatalf("version %q err %v", got, err)
	}
}

func TestMSIXPublisherMustMatchExactly(t *testing.T) {
	for _, tc := range []struct{ publisher, agent string }{
		{claudePublisher, "claude"},
		{"CN=0ED83B57-1FA9-465C-98F8-41F190D0DF7F", "claude"},
		{codexPublisher, "codex"},
	} {
		if !msixPublisherAllowed(tc.publisher, tc.agent) {
			t.Errorf("official publisher rejected: %s", tc.publisher)
		}
	}
	for _, tc := range []struct{ publisher, agent string }{
		{"CN=Not Anthropic Ltd", "claude"},
		{`CN="Anthropic, PBC"`, "claude"},
		{"CN=OpenAI Lookalike", "codex"},
		{claudePublisher, "codex"},
	} {
		if msixPublisherAllowed(tc.publisher, tc.agent) {
			t.Errorf("lookalike publisher accepted: %s", tc.publisher)
		}
	}
}

// One unreachable Store must not cost each desktop app its own long wait.
func TestStoreOutageIsSharedBetweenApps(t *testing.T) {
	m := newToolManager()
	m.root = t.TempDir()
	m.storeUnavailable = map[string]time.Time{}
	m.markStoreUnavailable("claude")
	if !m.storeUnavailableFor("codex") {
		t.Fatal("Codex would wait on a Store that Claude just found unavailable")
	}
	m.clearStoreUnavailable("codex")
	if m.storeUnavailableFor("claude") {
		t.Fatal("a working Store stayed marked unavailable")
	}
}

func TestGUIDiskCheckFailsBeforeDownload(t *testing.T) {
	low := func(string) (int64, error) { return 1 << 30, nil }
	err := checkGUIDisk("codex", "C:\\tools", low)
	if err == nil || !strings.Contains(installErrorMessage(err), "磁盘空间不足") {
		t.Fatalf("low disk: %v", err)
	}
	if err := checkGUIDisk("claude", "C:\\tools", low); err == nil {
		t.Fatal("1 GiB is below the Claude requirement")
	}
	plenty := func(string) (int64, error) { return 8 << 30, nil }
	if err := checkGUIDisk("codex", "C:\\tools", plenty); err != nil {
		t.Fatal(err)
	}
	// A failed probe must never block an install that might well succeed.
	unknown := func(string) (int64, error) { return 0, errors.New("probe failed") }
	if err := checkGUIDisk("codex", "C:\\tools", unknown); err != nil {
		t.Fatal(err)
	}
}

func TestFreeDiskBytesReportsThisVolume(t *testing.T) {
	if n, err := freeDiskBytes(t.TempDir()); err != nil || n <= 0 {
		t.Fatalf("free %d err %v", n, err)
	}
}

// The Codex mirror ships Codex-mac-*.dmg while the Cask ships ChatGPT.app.
func TestFindGUIAppAcceptsEitherBundleName(t *testing.T) {
	dir := t.TempDir()
	if got := findGUIApp(dir, "codex"); got != "" {
		t.Fatalf("empty stage returned %q", got)
	}
	if err := os.Mkdir(filepath.Join(dir, "Codex.app"), 0700); err != nil {
		t.Fatal(err)
	}
	if got := findGUIApp(dir, "codex"); got != "Codex.app" {
		t.Fatalf("got %q", got)
	}
	if got := findGUIApp(dir, "claude"); got != "" {
		t.Fatalf("Codex bundle accepted as Claude: %q", got)
	}
}
