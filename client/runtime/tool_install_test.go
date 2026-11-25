package clientruntime

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestToolDownloadPolicy(t *testing.T) {
	for _, u := range []string{"http://registry.npmmirror.com/a", "https://evil.example/a", "https://user:password@downloads.claude.ai/a", "https://downloads.claude.ai.evil.example/a", "https://downloads.claude.ai:443/a", "https://downloads.claude.ai/a#fragment"} {
		if allowedDownloadURL(u) {
			t.Fatal(u)
		}
	}
	for _, u := range []string{npmMirror + "/@openai/codex/latest", "https://downloads.claude.ai/releases/a.msix", "https://persistent.oaistatic.com/codex-app-prod/ChatGPT-x64.msix", "https://store.rg-adguard.net/api/GetFiles", "https://tlu.dl.delivery.mp.microsoft.com/filestreamingservice/files/a?P1=1", "https://claudeapp.agentsmirror.com/latest/mac", "https://codexapp.agentsmirror.com/latest/win-x64", "https://fgws3-ocloud.ihep.ac.cn/20830-codex/latest/win-x64?X-Amz-Signature=abc"} {
		if !allowedDownloadURL(u) {
			t.Fatal(u)
		}
	}
	for _, u := range []string{"https://agentsmirror.com/latest/win-x64", "https://codexapp.agentsmirror.com.evil.example/a", "https://fgws3-ocloud.ihep.ac.cn:8443/a"} {
		if allowedDownloadURL(u) {
			t.Fatal(u)
		}
	}
	if allowedDownloadURL("http://tlu.dl.delivery.mp.microsoft.com/filestreamingservice/files/a") {
		t.Fatal("insecure Microsoft CDN URL accepted")
	}
	if !allowedMicrosoftCDNHTTP("http://tlu.dl.delivery.mp.microsoft.com/filestreamingservice/files/a?P1=1") || allowedMicrosoftCDNHTTP("http://evil.example/a") {
		t.Fatal("Microsoft HTTP compatibility policy incorrect")
	}
}
func TestWingetStoreProbeUsesSupportedFlag(t *testing.T) {
	got := strings.Join(wingetStoreProbeArgs(), " ")
	if got != "source list --name msstore" {
		t.Fatalf("unexpected Store probe command: %q", got)
	}
}
func TestToolDownloadResumesAndReusesVerifiedCache(t *testing.T) {
	body := bytes.Repeat([]byte("download payload"), 1024)
	sum := sha256.Sum256(body)
	var calls atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("ETag", "\"one\"")
		if r.Header.Get("Range") == "" {
			w.Header().Set("Content-Length", fmt.Sprint(len(body)))
			w.WriteHeader(200)
			_, _ = w.Write(body[:1000])
			w.(http.Flusher).Flush()
			c, _, _ := w.(http.Hijacker).Hijack()
			c.Close()
			return
		}
		if r.Header.Get("Range") != "bytes=1000-" || r.Header.Get("If-Range") != "\"one\"" {
			t.Errorf("bad resume headers: %v", r.Header)
		}
		w.Header().Set("Content-Range", fmt.Sprintf("bytes 1000-%d/%d", len(body)-1, len(body)))
		w.WriteHeader(206)
		_, _ = w.Write(body[1000:])
	}))
	defer s.Close()
	d := &toolDownloader{client: s.Client(), cache: t.TempDir(), allow: func(u string) bool { return strings.HasPrefix(u, s.URL+"/") }}
	p, e := d.file(context.Background(), s.URL+"/a", hex.EncodeToString(sum[:]), func(string) {})
	if e != nil {
		t.Fatal(e)
	}
	b, _ := os.ReadFile(p)
	if !bytes.Equal(b, body) || calls.Load() != 2 {
		t.Fatal("resume did not restore full payload")
	}
	if _, e = d.file(context.Background(), s.URL+"/a", hex.EncodeToString(sum[:]), func(string) {}); e != nil || calls.Load() != 2 {
		t.Fatal("verified cache was redownloaded")
	}
}
func TestToolDownloadCancellationAndChecksum(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("first"))
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer s.Close()
	d := &toolDownloader{client: s.Client(), cache: t.TempDir(), allow: func(string) bool { return true }}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	start := time.Now()
	_, e := d.file(ctx, s.URL+"/a", strings.Repeat("0", 64), func(string) { cancel() })
	if !errors.Is(e, context.Canceled) || time.Since(start) > 2*time.Second {
		t.Fatalf("cancel: %v", e)
	}
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("corrupt")) }))
	defer bad.Close()
	d.client = bad.Client()
	if _, e = d.file(context.Background(), bad.URL+"/a", strings.Repeat("0", 64), func(string) {}); e == nil {
		t.Fatal("corrupt binary accepted")
	}
}
func TestToolArchiveRejectsTraversalAndSymlinkChains(t *testing.T) {
	for _, name := range []string{"../../escape", "/absolute", "C:/windows/file", "a\\..\\escape"} {
		if _, e := archivePath(t.TempDir(), name); e == nil {
			t.Fatal(name)
		}
	}
	var b bytes.Buffer
	gz := gzip.NewWriter(&b)
	tw := tar.NewWriter(gz)
	_ = tw.WriteHeader(&tar.Header{Name: "node/bin/evil", Typeflag: tar.TypeSymlink, Linkname: "../../outside", Mode: 0700})
	tw.Close()
	gz.Close()
	p := filepath.Join(t.TempDir(), "node.tgz")
	_ = os.WriteFile(p, b.Bytes(), 0600)
	if e := extractToolArchive(p, t.TempDir(), "tar.gz"); e == nil {
		t.Fatal("arbitrary symlink accepted")
	}
}
func TestNPMEntryRejectsEscapingBinAndSupportsPrivateNode(t *testing.T) {
	prefix := t.TempDir()
	root := filepath.Join(prefix, "lib", "node_modules", "@openai", "codex")
	_ = os.MkdirAll(root, 0700)
	_ = os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"bin":{"codex":"../../evil.js"}}`), 0600)
	if _, e := npmEntry(prefix, "/private/node", "codex", "darwin"); e == nil {
		t.Fatal("escaping package bin accepted")
	}
	_ = os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"bin":{"codex":"bin/codex.js"}}`), 0600)
	e, err := npmEntry(prefix, "/private/node", "codex", "darwin")
	if err != nil || e.Program != "/private/node" || len(e.Args) != 1 || !pathInside(root, e.Args[0]) {
		t.Fatalf("%+v %v", e, err)
	}
}
func TestMSIXIdentityAndArchitecture(t *testing.T) {
	makePackage := func(name, arch string) string {
		var b bytes.Buffer
		z := zip.NewWriter(&b)
		f, _ := z.Create("AppxManifest.xml")
		publisher := codexPublisher
		if strings.Contains(name, "Anthropic") {
			publisher = claudePublisher
		}
		_, _ = fmt.Fprintf(f, `<Package><Identity Name="%s" Publisher="%s" ProcessorArchitecture="%s" /></Package>`, name, publisher, arch)
		z.Close()
		p := filepath.Join(t.TempDir(), "app.msix")
		_ = os.WriteFile(p, b.Bytes(), 0600)
		return p
	}
	if e := validateMSIX(makePackage("OpenAI.Codex", "arm64"), "codex", "arm64"); e != nil {
		t.Fatal(e)
	}
	for _, p := range []string{makePackage("OpenAI.ChatGPT", "x64"), makePackage("OpenAI.Codex", "arm64")} {
		if e := validateMSIX(p, "codex", "amd64"); e == nil {
			t.Fatal("wrong product or CPU accepted")
		}
	}
}
func TestMSIXStorePublisherGUIDAccepted(t *testing.T) {
	// Both GUIDs were read from the vendors' real packages on 2026-10-29: the
	// official codex-app-prod MSIX and the Store Claude package. Store signing
	// replaces the vendor certificate subject with a registry GUID, so the old
	// name-substring check rejected the genuine packages.
	for _, tc := range []struct{ publisher, agent string }{
		{"CN=50BDFD77-8903-4850-9FFE-6E8522F64D5B", "codex"},
		{"CN=0ED83B57-1FA9-465C-98F8-41F190D0DF7F", "claude"},
	} {
		if !msixPublisherAllowed(tc.publisher, tc.agent) {
			t.Fatalf("official Store publisher rejected for %s: %s", tc.agent, tc.publisher)
		}
	}
	for _, tc := range []struct{ publisher, agent string }{
		{"CN=50BDFD77-8903-4850-9FFE-6E8522F64D6C", "codex"}, // near-miss GUID
		{"CN=0ED83B57-1FA9-465C-98F8-41F190D0DF7F", "codex"}, // Claude GUID on Codex
		{"CN=50BDFD77-8903-4850-9FFE-6E8522F64D5B", "claude"},
	} {
		if msixPublisherAllowed(tc.publisher, tc.agent) {
			t.Fatalf("unknown publisher accepted for %s: %s", tc.agent, tc.publisher)
		}
	}
}
func TestClaudeStoreIdentityAndMirrorParser(t *testing.T) {
	if !msixIdentityAllowed("AnthropicPBC.Claude", "claude") || !msixIdentityAllowed("Claude", "claude") || !msixPublisherAllowed(claudePublisher, "claude") {
		t.Fatal("Claude Store identities not accepted")
	}
	if msixIdentityAllowed("OpenAI.Codex", "claude") {
		t.Fatal("foreign identity accepted for Claude")
	}
	body := []byte(`<table><tr><td><a href="http://tlu.dl.delivery.mp.microsoft.com/filestreamingservice/files/abc?P1=1&amp;P2=404">AnthropicPBC.Claude_3.6556.2.0_arm64__fnn82j28hfe8t.msix</a></td><td>0000000000000000000000000000000000000000</td></tr><tr><td><a href="http://tlu.dl.delivery.mp.microsoft.com/filestreamingservice/files/def?P1=2&amp;P2=404">AnthropicPBC.Claude_3.6556.2.0_x64__fnn82j28hfe8t.msix</a></td><td>e44ee881c37accc8dcec0265723661f89e6cbb34</td></tr></table>`)
	r, err := parseStoreMirrorMSIX(body, "amd64")
	if err != nil {
		t.Fatal(err)
	}
	if r.URL != "https://tlu.dl.delivery.mp.microsoft.com/filestreamingservice/files/def?P1=2&P2=404" || r.FallbackURL != "http://tlu.dl.delivery.mp.microsoft.com/filestreamingservice/files/def?P1=2&P2=404" || r.SHA1 != "e44ee881c37accc8dcec0265723661f89e6cbb34" || r.Format != "msix" {
		t.Fatalf("unexpected mirror release: %+v", r)
	}
}
func TestInstallTasksAreIndependentAndCancellationIsTargeted(t *testing.T) {
	g := &fakeGW{models: 200}
	gw := httptest.NewServer(g.handler())
	defer gw.Close()
	a, _ := testDesktopApp(t, gw)
	started := make(chan string, 2)
	a.env.InstallDesktop = func(ctx context.Context, agent, component string, p func(string)) error {
		started <- agent
		p("working")
		<-ctx.Done()
		return ctx.Err()
	}
	for _, agent := range []string{"claude", "codex"} {
		if e := a.DesktopAction(DesktopRequest{Action: "install-suite", Agent: agent}); e != nil {
			t.Fatal(e)
		}
	}
	for i := 0; i < 2; i++ {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("background install did not start")
		}
	}
	if e := a.DesktopAction(DesktopRequest{Action: "home"}); e != nil {
		t.Fatal(e)
	}
	a.cancelDesktopInstall("claude")
	a.installMu.Lock()
	one, two := a.installations["claude"], a.installations["codex"]
	a.installMu.Unlock()
	select {
	case <-one.done:
	case <-time.After(time.Second):
		t.Fatal("cancel queued behind installer")
	}
	select {
	case <-two.done:
		t.Fatal("cancel stopped unrelated application")
	default:
	}
	a.cancelDesktopInstall("codex")
	<-two.done
	if !a.installTaskSnapshot()[0].Cancelled {
		t.Fatal("cancel status missing")
	}
}
func TestGUIProfileIsReversibleAndDoesNotOverwriteOtherProfiles(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("registry and running GUI are covered by Windows acceptance")
	}
	g := &fakeGW{models: 200}
	gw := httptest.NewServer(g.handler())
	defer gw.Close()
	_, _ = testDesktopApp(t, gw)
	old := TestOrigin
	TestOrigin = gw.URL
	defer func() { TestOrigin = old }()
	files := desktopGUIFiles()
	_ = os.MkdirAll(filepath.Dir(files[0]), 0700)
	original := []byte(`{"mcpServers":{"keep":{}}}`)
	_ = os.WriteFile(files[0], original, 0600)
	_ = os.MkdirAll(filepath.Dir(files[3]), 0700)
	_ = os.WriteFile(files[3], []byte(`{"entries":[{"id":"other","name":"Keep"}],"appliedId":"other"}`), 0600)
	if e := configureDesktopGUI("claude", gw.URL, fakeKey); e != nil {
		t.Fatal(e)
	}
	if e := configureDesktopGUI("claude", gw.URL, "rotated-key"); e != nil {
		t.Fatal(e)
	}
	var meta map[string]any
	b, _ := os.ReadFile(files[3])
	_ = json.Unmarshal(b, &meta)
	if len(meta["entries"].([]any)) != 2 {
		t.Fatal("other profile lost or duplicated")
	}
	if desktopGUIEdited() {
		t.Fatal("unchanged GUI config marked edited")
	}
	if e := restoreDesktopGUI(); e != nil {
		t.Fatal(e)
	}
	b, _ = os.ReadFile(files[0])
	if !bytes.Equal(b, original) {
		t.Fatal("original GUI backup was overwritten")
	}
	if _, e := os.Stat(files[2]); !os.IsNotExist(e) {
		t.Fatal("managed profile retained after restore")
	}
}
func TestHomeDoesNotContactNetwork(t *testing.T) {
	g := &fakeGW{models: 200}
	gw := httptest.NewServer(g.handler())
	defer gw.Close()
	a, _ := testDesktopApp(t, gw)
	a.env.HTTP = &http.Client{Transport: roundTripFailure{t}}
	if e := a.DesktopAction(DesktopRequest{Action: "home"}); e != nil {
		t.Fatal(e)
	}
}

type roundTripFailure struct{ t *testing.T }

func (r roundTripFailure) RoundTrip(*http.Request) (*http.Response, error) {
	r.t.Error("closing modal contacted the network")
	return nil, io.EOF
}
func TestToolLiveMirrorCLIInstall(t *testing.T) {
	if os.Getenv("JIESHANGAI_LIVE_INSTALL") != "1" {
		t.Skip("explicit network integration test")
	}
	m := newToolManager()
	m.root = t.TempDir()
	m.isolate = true
	m.download.cache = filepath.Join(m.root, "cache")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	agent := os.Getenv("JIESHANGAI_LIVE_AGENT")
	if !validAgent(agent) {
		agent = "codex"
	}
	if e := m.installCLI(ctx, agent, false, func(line string) { t.Log(line) }); e != nil {
		t.Fatal(e)
	}
	if !m.cliReady(agent) {
		t.Fatal("live package not runnable")
	}
	if _, e := m.readEntry(agent); !e {
		t.Fatal("no managed installation receipt")
	}
}
func TestToolVersionComparison(t *testing.T) {
	for _, pair := range [][2]string{{"2.1.311", "2.1.9"}, {"26.1021.10", "26.623.9"}} {
		if !newerVersion(pair[0], pair[1]) || newerVersion(pair[1], pair[0]) {
			t.Fatal(pair)
		}
	}
}

func TestStoreFallbackIsRememberedForRetryAndExpires(t *testing.T) {
	m := newToolManager()
	m.root = t.TempDir()
	m.storeUnavailable = map[string]time.Time{}
	if m.storeUnavailableFor("claude") {
		t.Fatal("fresh manager unexpectedly skipped Store")
	}
	m.markStoreUnavailable("claude")
	if !m.storeUnavailableFor("claude") {
		t.Fatal("Store failure was not remembered")
	}
	until, err := os.ReadFile(filepath.Join(m.root, "claude", "gui-store-unavailable-until"))
	if err != nil || strings.TrimSpace(string(until)) == "" {
		t.Fatalf("fallback marker was not persisted: %v", err)
	}
	m.mu.Lock()
	m.storeUnavailable["claude"] = time.Now().Add(-time.Minute)
	m.mu.Unlock()
	if m.storeUnavailableFor("claude") {
		t.Fatal("expired Store fallback still blocked the source")
	}
	m.clearStoreUnavailable("claude")
	if _, err := os.Stat(filepath.Join(m.root, "claude", "gui-store-unavailable-until")); !os.IsNotExist(err) {
		t.Fatalf("fallback marker was not cleared: %v", err)
	}
}

func TestToolVendorCacheRequiresETagAndLocalHash(t *testing.T) {
	var downloads, unchanged atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"release-1"`)
		if r.Header.Get("If-None-Match") == `"release-1"` {
			unchanged.Add(1)
			w.WriteHeader(304)
			return
		}
		downloads.Add(1)
		_, _ = w.Write([]byte("official msix"))
	}))
	defer s.Close()
	// Not t.TempDir(): when antivirus denies deleting the cached file, TempDir's
	// mandatory cleanup would fail the test even after the skip below.
	cache, err := os.MkdirTemp("", "toolvendorcache-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(cache) })
	d := &toolDownloader{client: s.Client(), cache: cache, allow: func(string) bool { return true }}
	p, e := d.file(context.Background(), s.URL+"/latest.msix", "", func(string) {})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = d.file(context.Background(), s.URL+"/latest.msix", "", func(string) {}); e != nil || downloads.Load() != 1 || unchanged.Load() != 1 {
		t.Fatal("conditional cache failed", e)
	}
	// Antivirus software can hold a freshly downloaded file for seconds, so the
	// corruption step retries. Some products deny every access to the file for
	// the whole test-process lifetime (observed on one Windows 11 machine; a
	// standalone replica of the same write/rename/ACL sequence was untouched),
	// which no budget can outwait — skip rather than fail there. CI runners
	// without third-party antivirus exercise the full path.
	var werr error
	for delay := 100 * time.Millisecond; ; delay *= 2 {
		if werr = os.WriteFile(p, []byte("corrupt"), 0600); werr == nil {
			break
		}
		if delay > 1600*time.Millisecond {
			if os.IsPermission(werr) {
				t.Skipf("machine antivirus denies test access to the cached file: %v", werr)
			}
			t.Fatal(werr)
		}
		time.Sleep(delay)
	}
	if _, e = d.file(context.Background(), s.URL+"/latest.msix", "", func(string) {}); e != nil || downloads.Load() != 2 {
		t.Fatal("corrupted cache reused", e)
	}
}
func TestOfflinePayloadHashAndPath(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "payload")
	_ = os.WriteFile(p, []byte("vendor"), 0600)
	hash, _ := fileDigest(p)
	b := offlineBundle{Files: map[string]string{"payload": hash, "../outside": hash}}
	if _, e := offlineFile(dir, b, "payload"); e != nil {
		t.Fatal(e)
	}
	if _, e := offlineFile(dir, b, "../outside"); e == nil {
		t.Fatal("traversal accepted")
	}
	_ = os.WriteFile(p, []byte("changed"), 0600)
	if _, e := offlineFile(dir, b, "payload"); e == nil {
		t.Fatal("corrupt payload accepted")
	}
}
func TestDesktopZipRejectsEscapingSymlink(t *testing.T) {
	p := filepath.Join(t.TempDir(), "desktop.zip")
	f, _ := os.Create(p)
	w := zip.NewWriter(f)
	h := &zip.FileHeader{Name: "Claude.app/escape"}
	h.SetMode(os.ModeSymlink | 0777)
	r, _ := w.CreateHeader(h)
	_, _ = r.Write([]byte("../../outside"))
	_ = w.Close()
	_ = f.Close()
	if e := validateGUIZIP(p, t.TempDir()); e == nil {
		t.Fatal("unsafe app symlink accepted")
	}
}

func TestInstallFailurePreservesWindowsCodeAndRedactsOutput(t *testing.T) {
	f := processFailure("install", "msstore", "Add-AppxPackage failed 0x80073D28 Authorization: Bearer secret-value", errors.New("exit status 1"))
	if f.Code != "permission" || f.NativeCode != "0X80073D28" || f.Retryable {
		t.Fatalf("unexpected failure: %+v", f)
	}
	if strings.Contains(redactInstallText("Authorization: Bearer secret-value"), "secret-value") {
		t.Fatal("diagnostic leaked credential")
	}
}
func TestInstallFailureClassifiesNetworkAndNpmSeparately(t *testing.T) {
	network := processFailure("npm", "npmmirror", "npm ERR! code EACCES", errors.New("exit status 1"))
	if network.Code != "filesystem" {
		t.Fatalf("npm filesystem was classified as %s", network.Code)
	}
	dns := processFailure("download", "official", "Get https://downloads.claude.ai: dial tcp: lookup host: no such host", errors.New("lookup"))
	if dns.Code != "dns" || dns.Message == "" {
		t.Fatalf("dns was not classified: %+v", dns)
	}
}
func TestInstallFailureClassifiesElevationDenialAsRetryable(t *testing.T) {
	f := processFailure("install", "https://persistent.oaistatic.com", "JIESHANGAI_ELEVATION_FAILED", errors.New("exit status 1"))
	if f.Code != "permission" || !f.Retryable {
		t.Fatalf("unexpected elevation failure: %+v", f)
	}
}
func TestInstallFailureKeepsElevationHintWhenNativeCodePresent(t *testing.T) {
	// Field shape: the per-user attempt reported 0x80073D28 (packaged service
	// needs admin), the follow-up elevation was dismissed, and both outputs are
	// concatenated. The retry hint must survive — the bare native-code message
	// used to overwrite it because the switch runs after the marker check.
	combined := "Add-AppxPackage : 0x80073D28\nJIESHANGAI_ELEVATION_FAILED uac-dismissed"
	f := processFailure("install", "agentsmirror", combined, errors.New("exit status 1"))
	if f.Code != "permission" || !f.Retryable {
		t.Fatalf("unexpected elevation failure: %+v", f)
	}
	if !strings.Contains(f.Message, "点击重试") {
		t.Fatalf("elevation retry hint was lost: %s", f.Message)
	}
}

// Reproduces the Win10 beta.5 codex report: UAC was approved and the elevated
// Add-AppxPackage failed with disk-full (0x80073CF4 / 0x80070070 after the
// JIESHANGAI_APPX_ERROR marker), yet the UI demanded admin authorization.
// Classification took the FIRST native code in the combined output — the
// stale per-user 0x80073D28 that elevation had already superseded — and the
// elevation marker then overrode everything else, so the disk branch was
// unreachable for elevated failures.
func TestInstallFailurePrefersElevatedAppxErrorCode(t *testing.T) {
	combined := "Add-AppxPackage : deployment failed 0x80073D28\r\n" +
		"JIESHANGAI_APPX_START\r\n" +
		"JIESHANGAI_APPX_ERROR: 部署操作失败 HRESULT: 0x80073CF4 0x80070070 部署磁盘空间不足\r\n" +
		"JIESHANGAI_ELEVATION_FAILED exit=3"
	f := processFailure("install", "mirror", combined, errors.New("exit status 3"))
	if f.Code != "disk" || f.NativeCode != "0X80073CF4" {
		t.Fatalf("elevated disk-full was masked: %+v", f)
	}
	if !strings.Contains(f.Message, "磁盘空间不足") {
		t.Fatalf("message lost the disk hint: %s", f.Message)
	}
	if !f.Retryable {
		t.Fatal("freeing space and retrying must stay possible after an elevation attempt")
	}
}

// An elevated code the table doesn't know must fall back to the honest
// generic message instead of claiming admin authorization is still missing.
func TestInstallFailureUnknownElevatedCodeIsNotPermission(t *testing.T) {
	combined := "0x80073D28\r\nJIESHANGAI_APPX_ERROR: HRESULT: 0x80073CF9\r\nJIESHANGAI_ELEVATION_FAILED exit=3"
	f := processFailure("install", "mirror", combined, errors.New("exit status 3"))
	if f.Code != "installation" || f.NativeCode != "0X80073CF9" {
		t.Fatalf("unknown elevated code must stay generic, got %+v", f)
	}
}

func TestComponentRetryDoesNotRetainPreviousSource(t *testing.T) {
	m := newToolManager()
	m.states["codex"] = ToolStatus{Agent: "codex", CLIInstall: &ComponentStatus{Status: "failed", Source: "offline", Phase: "install"}}
	m.component("codex", "cli", "running", "正在准备安装", nil)
	if got := m.states["codex"].CLIInstall; got == nil || got.Source != "npmmirror" || got.Phase != "prepare" {
		t.Fatalf("stale install state retained: %+v", got)
	}
}
func TestToolComponentStatusPersistsSourceAndDiagnosticState(t *testing.T) {
	m := newToolManager()
	m.root = t.TempDir()
	m.states["codex"] = ToolStatus{Agent: "codex", GUIAvailable: true}
	m.component("codex", "gui", "running", "正在通过微软商店安装官方桌面版……", nil)
	m.componentError("codex", "gui", processFailure("install", "msstore", "0x80073D28", errors.New("failure")))
	s := m.snapshot()[1]
	if s.GUIInstall == nil || s.GUIInstall.Source != "msstore" || s.GUIInstall.Failure == nil || s.GUIInstall.Failure.Code != "permission" {
		t.Fatalf("component state missing: %+v", s.GUIInstall)
	}
}

// Reproduces the beta.5 field report: a CLI failure wrapped in the generic
// message showed source "offline" (keyword match on "离线安装包") and hid the
// underlying error entirely.
func TestToolComponentErrorKeepsCauseAndHonestSource(t *testing.T) {
	m := newToolManager()
	m.root = t.TempDir()
	m.states["claude"] = ToolStatus{Agent: "claude"}
	underlying := errors.New("写入命令行 PATH 失败： Access is denied")
	m.componentError("claude", "cli", underlying)
	st := m.snapshot()[0]
	if st.CLIInstall == nil || st.CLIInstall.Failure == nil {
		t.Fatal("failure record missing")
	}
	f := st.CLIInstall.Failure
	if f.Detail == "" || !strings.Contains(f.Detail, "Access is denied") {
		t.Fatalf("underlying cause not retained: %+v", f)
	}
	if st.CLIInstall.Source == "offline" {
		t.Fatalf("generic message mislabeled the source as offline: %+v", st.CLIInstall)
	}
}
