package clientruntime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const npmMirror = "https://registry.npmmirror.com"

var downloadHosts = map[string]bool{
	"registry.npmmirror.com": true, "npmmirror.com": true, "cdn.npmmirror.com": true,
	"nodejs.org": true, "formulae.brew.sh": true, "mirrors.tuna.tsinghua.edu.cn": true,
	"downloads.claude.ai": true, "claude.ai": true, "persistent.oaistatic.com": true,
	"store.rg-adguard.net":         true,
	"dl.delivery.mp.microsoft.com": true, "tlu.dl.delivery.mp.microsoft.com": true,
	"api.github.com": true, "raw.githubusercontent.com": true,
	// Third-party mirror of the vendors' own desktop archives, manifest
	// identity compared on 2026-10-29 and reachable directly from mainland China
	// where claude.ai is geo-blocked. Package integrity is still enforced after
	// download by the MSIX identity/signature and macOS codesign/spctl checks.
	"claudeapp.agentsmirror.com": true, "codexapp.agentsmirror.com": true,
	"claudeapp-r2.agentsmirror.com": true, "codexapp-r2.agentsmirror.com": true,
	// Presigned redirect target of the mirror's mainland-China object-storage node.
	"fgws3-ocloud.ihep.ac.cn": true,
}

func isAgentsMirrorSubdomain(host string) bool {
	if !strings.HasSuffix(host, ".agentsmirror.com") {
		return false
	}
	sub := strings.TrimSuffix(host, ".agentsmirror.com")
	if sub == "" || strings.Contains(sub, ".") {
		return false
	}
	return strings.HasPrefix(sub, "claudeapp") || strings.HasPrefix(sub, "codexapp")
}

func allowedDownloadURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" || u.Fragment != "" {
		return false
	}
	host := strings.ToLower(u.Hostname())
	return downloadHosts[host] || isAgentsMirrorSubdomain(host)
}

// allowedOverrideMirrorURL allows HTTPS URLs configured by the site operator in
// <origin>/client-dist/gui-mirrors.json (e.g. a reverse proxy or CDN). Package
// safety still relies unconditionally on OS signature verification after download.
func allowedOverrideMirrorURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" || u.Fragment != "" {
		return false
	}
	host := strings.ToLower(u.Hostname())
	return host != "" && host != "localhost" && !strings.HasPrefix(host, "127.") && host != "::1"
}

// renameWithRetry tolerates antivirus software briefly holding the replaced
// file on Windows; without it a completed multi-gigabyte transfer would be
// discarded over a momentary sharing violation.
func renameWithRetry(oldpath, newpath string) error {
	var err error
	for delay := 100 * time.Millisecond; ; delay *= 2 {
		if err = os.Rename(oldpath, newpath); err == nil {
			return nil
		}
		if delay > 1600*time.Millisecond {
			return err
		}
		time.Sleep(delay)
	}
}

func newDownloadClient() *http.Client {
	return newDownloadClientWithAllow(allowedDownloadURL)
}

func newDownloadClientWithAllow(allow func(string) bool) *http.Client {
	return &http.Client{Transport: &http.Transport{Proxy: systemAwareProxy, ResponseHeaderTimeout: 15 * time.Second, TLSHandshakeTimeout: 10 * time.Second, IdleConnTimeout: 30 * time.Second}, CheckRedirect: func(r *http.Request, via []*http.Request) error {
		if len(via) >= 5 || !allow(r.URL.String()) {
			return errors.New("download redirect rejected")
		}
		return nil
	}}
}

// cachedCard mirrors the on-disk ETag sidecar. Antivirus software can block
// re-reading a freshly written file for seconds, so the in-process record from
// the download that produced the file backs the disk sidecar up while this
// downloader lives; across restarts only the sidecar is consulted.
type cachedCard struct {
	ETag   string `json:"etag"`
	SHA256 string `json:"sha256"`
}

type toolDownloader struct {
	client *http.Client
	cache  string
	allow  func(string) bool
	locks  sync.Map
	slots  chan struct{}
	fresh  sync.Map // cache-file path -> cachedCard
}

func (d *toolDownloader) metadata(ctx context.Context, raw string, target any) error {
	if !d.allow(raw) {
		return errors.New("download source rejected")
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	r, _ := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	r.Header.Set("User-Agent", "JieshangAI-Installer/0.2")
	response, err := d.client.Do(r)
	if err != nil {
		return fmt.Errorf("%w: metadata", errInstallNetwork)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("%w: metadata status %d", errInstallNetwork, response.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(response.Body, 8<<20)).Decode(target)
}

type stalledBody struct {
	io.ReadCloser
	mu    sync.Mutex
	timer *time.Timer
}

func watchBody(body io.ReadCloser) *stalledBody {
	s := &stalledBody{ReadCloser: body}
	s.timer = time.AfterFunc(30*time.Second, func() { _ = body.Close() })
	return s
}
func (s *stalledBody) Read(p []byte) (int, error) {
	n, e := s.ReadCloser.Read(p)
	s.mu.Lock()
	if n > 0 {
		s.timer.Reset(30 * time.Second)
	}
	s.mu.Unlock()
	return n, e
}
func (s *stalledBody) Close() error {
	s.mu.Lock()
	s.timer.Stop()
	s.mu.Unlock()
	return s.ReadCloser.Close()
}

// Disk streaming, resumable with If-Range. No installer credentials are sent.
func (d *toolDownloader) file(ctx context.Context, raw, checksum string, progress func(string)) (string, error) {
	if !d.allow(raw) {
		return "", errors.New("download source rejected")
	}
	lock, _ := d.locks.LoadOrStore(raw, make(chan struct{}, 1))
	entryLock := lock.(chan struct{})
	select {
	case entryLock <- struct{}{}:
		defer func() { <-entryLock }()
	case <-ctx.Done():
		return "", ctx.Err()
	}
	if d.slots != nil {
		select {
		case d.slots <- struct{}{}:
			defer func() { <-d.slots }()
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	if err := os.MkdirAll(d.cache, 0700); err != nil {
		return "", err
	}
	key := sha256.Sum256([]byte(raw))
	dest := filepath.Join(d.cache, hex.EncodeToString(key[:]))
	var cached cachedCard
	if checksum == "" {
		if data, e := os.ReadFile(dest + ".json"); e == nil && json.Unmarshal(data, &cached) == nil {
			hash, e := fileDigest(dest)
			if e != nil {
				// The digest read was blocked (observed: antivirus holds freshly
				// renamed files). If this downloader wrote the file itself, its
				// in-process record stands in for the unreadable bytes.
				if mem, ok := d.fresh.Load(dest); ok && mem.(cachedCard) == cached {
					hash, e = cached.SHA256, nil
				}
			}
			if e != nil || hash != cached.SHA256 {
				cached.ETag = ""
			}
		}
	}
	valid := func(path string) bool {
		if checksum == "" {
			return false
		}
		f, e := os.Open(path)
		if e != nil {
			return false
		}
		defer f.Close()
		h := sha256.New()
		_, e = io.Copy(h, f)
		return e == nil && strings.EqualFold(hex.EncodeToString(h.Sum(nil)), checksum)
	}
	if valid(dest) {
		progress("正在使用已校验的下载缓存……")
		return dest, nil
	}
	part := dest + ".part"
	if valid(part) {
		if e := renameWithRetry(part, dest); e == nil {
			return dest, nil
		}
	}
	var last error
	for attempt := 0; attempt < 2; attempt++ {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		offset := int64(0)
		etag, _ := os.ReadFile(part + ".etag")
		if st, e := os.Stat(part); e == nil && len(etag) > 0 {
			offset = st.Size()
		}
		r, _ := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
		r.Header.Set("User-Agent", "JieshangAI-Installer/0.2")
		if offset == 0 && checksum == "" && cached.ETag != "" {
			r.Header.Set("If-None-Match", cached.ETag)
		}
		if offset > 0 {
			r.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
			r.Header.Set("If-Range", string(etag))
		}
		res, err := d.client.Do(r)
		if err != nil {
			last = err
			continue
		}
		if res.StatusCode == http.StatusNotModified && cached.ETag != "" {
			res.Body.Close()
			progress("正在使用已校验的官方安装包缓存……")
			return dest, nil
		}
		if res.StatusCode != http.StatusOK && res.StatusCode != http.StatusPartialContent {
			res.Body.Close()
			last = fmt.Errorf("HTTP %d", res.StatusCode)
			continue
		}
		if res.StatusCode == http.StatusPartialContent {
			if !strings.HasPrefix(res.Header.Get("Content-Range"), fmt.Sprintf("bytes %d-", offset)) {
				res.Body.Close()
				return "", errors.New("invalid download range")
			}
		} else {
			offset = 0
		}
		flag := os.O_CREATE | os.O_WRONLY
		if offset > 0 {
			flag |= os.O_APPEND
		} else {
			flag |= os.O_TRUNC
		}
		f, err := os.OpenFile(part, flag, 0600)
		if err != nil {
			res.Body.Close()
			return "", err
		}
		_ = os.WriteFile(part+".etag", []byte(res.Header.Get("ETag")), 0600)
		body := watchBody(res.Body)
		total := res.ContentLength
		read := offset
		started := time.Now()
		lastUpdate := time.Time{}
		buf := make([]byte, 128<<10)
		for {
			n, e := body.Read(buf)
			if n > 0 {
				if _, err = f.Write(buf[:n]); err != nil {
					last = err
					break
				}
				read += int64(n)
				if lastUpdate.IsZero() || time.Since(lastUpdate) >= 300*time.Millisecond {
					elapsed := time.Since(started).Seconds()
					speed := 0.0
					if elapsed > 0.05 {
						speed = float64(read-offset) / elapsed / (1 << 20)
					}
					line := fmt.Sprintf("正在下载：%.1f MB", float64(read)/(1<<20))
					if total > 0 {
						full := total + offset
						pct := int(float64(read) * 100 / float64(full))
						if pct < 0 {
							pct = 0
						} else if pct > 100 {
							pct = 100
						}
						line = fmt.Sprintf("正在下载：%.1f / %.1f MB（%d%%）", float64(read)/(1<<20), float64(full)/(1<<20), pct)
					}
					progress(line + " · " + strconv.FormatFloat(speed, 'f', 1, 64) + " MB/s")
					lastUpdate = time.Now()
				}
			}
			if e != nil {
				if e == io.EOF {
					last = nil
				} else {
					last = e
				}
				break
			}
		}
		body.Close()
		closeErr := f.Close()
		if last == nil {
			last = closeErr
		}
		if last != nil {
			continue
		}
		if checksum != "" && !valid(part) {
			_ = os.Remove(part)
			_ = os.Remove(part + ".etag")
			return "", errors.New("下载文件校验失败，请重新下载")
		}
		if err = renameWithRetry(part, dest); err != nil {
			return "", err
		}
		if checksum == "" {
			cached.ETag = res.Header.Get("ETag")
			cached.SHA256, _ = fileDigest(dest)
			data, _ := json.Marshal(cached)
			_ = writePrivateFile(dest+".json", data)
			d.fresh.Store(dest, cached)
		}
		_ = os.Remove(part + ".etag")
		return dest, nil
	}
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	return "", fmt.Errorf("%w: transfer failed (%T)", errInstallNetwork, last)
}
