package clientruntime

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// The central directory of a real package is a few hundred KB. Fetching it and
// the manifest in large windows keeps a version check to a handful of requests;
// the mirror answers 429 to the hundred-odd small reads archive/zip would make.
const (
	msixTailBytes   = 4 << 20
	msixWindowBytes = 256 << 10
)

type remoteRange struct {
	ctx    context.Context
	d      *toolDownloader
	raw    string
	start  int64
	data   []byte
	total  int64
	window struct {
		start int64
		data  []byte
	}
}

func (r *remoteRange) fetch(spec string) (int64, []byte, error) {
	req, err := http.NewRequestWithContext(r.ctx, http.MethodGet, r.raw, nil)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("User-Agent", "JieshangAI-Installer/0.2")
	req.Header.Set("Range", "bytes="+spec)
	res, err := r.d.client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusPartialContent {
		return 0, nil, fmt.Errorf("range status %d", res.StatusCode)
	}
	var first, last, total int64
	if _, err = fmt.Sscanf(res.Header.Get("Content-Range"), "bytes %d-%d/", &first, &last); err != nil {
		return 0, nil, errors.New("invalid content range")
	}
	contentRange := res.Header.Get("Content-Range")
	if total, err = strconv.ParseInt(contentRange[strings.LastIndex(contentRange, "/")+1:], 10, 64); err != nil || total <= 0 {
		return 0, nil, errors.New("invalid content range")
	}
	r.total = total
	body, err := io.ReadAll(io.LimitReader(res.Body, msixTailBytes+msixWindowBytes))
	return first, body, err
}

func serveFrom(p []byte, off, start int64, data []byte) (int, bool) {
	if off < start || off >= start+int64(len(data)) || off+int64(len(p)) > start+int64(len(data)) {
		return 0, false
	}
	return copy(p, data[off-start:]), true
}

func (r *remoteRange) ReadAt(p []byte, off int64) (int, error) {
	if n, ok := serveFrom(p, off, r.start, r.data); ok {
		return n, nil
	}
	if n, ok := serveFrom(p, off, r.window.start, r.window.data); ok {
		return n, nil
	}
	size := int64(len(p))
	if size < msixWindowBytes {
		size = msixWindowBytes
	}
	end := off + size - 1
	if end >= r.total {
		end = r.total - 1
	}
	start, data, err := r.fetch(fmt.Sprintf("%d-%d", off, end))
	if err != nil {
		return 0, err
	}
	r.window.start, r.window.data = start, data
	if off < start || off >= start+int64(len(data)) {
		return 0, io.ErrUnexpectedEOF
	}
	n := copy(p, data[off-start:])
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}

// remoteMSIXVersion reads the package identity without downloading the package,
// and applies the install-time identity rules so a foreign package is never
// offered as an update.
func (d *toolDownloader) remoteMSIXVersion(ctx context.Context, raw, agent, arch string) (string, error) {
	if !d.allow(raw) {
		return "", errors.New("download source rejected")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	r := &remoteRange{ctx: ctx, d: d, raw: raw}
	start, tail, err := r.fetch(fmt.Sprintf("-%d", msixTailBytes))
	if err != nil {
		return "", err
	}
	r.start, r.data = start, tail
	z, err := zip.NewReader(r, r.total)
	if err != nil {
		return "", err
	}
	identity, err := readMSIXIdentity(z.File)
	if err != nil {
		return "", err
	}
	if err = checkMSIXIdentity(identity, agent, arch); err != nil {
		return "", err
	}
	if identity.Version == "" {
		return "", errors.New("桌面版安装包缺少版本信息")
	}
	return identity.Version, nil
}

// firstRemoteMSIXVersion tries sources in order: one source being rate limited
// or blocked in this network must not hide an available update.
func (d *toolDownloader) firstRemoteMSIXVersion(ctx context.Context, sources []string, agent, arch string) (string, error) {
	var failures []error
	for _, raw := range sources {
		version, err := d.remoteMSIXVersion(ctx, raw, agent, arch)
		if err == nil {
			return version, nil
		}
		failures = append(failures, err)
		if ctx.Err() != nil {
			break
		}
	}
	return "", errors.Join(failures...)
}
