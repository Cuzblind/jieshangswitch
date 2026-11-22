package core

// Backup + fingerprint + restore (TRD §11.0 / M4). Shared with
// install/sh/install.sh (/i) and install/sh/uninstall.sh (/u), which read and
// write the same manifest byte-for-byte.
//
// Manifest: ~/.jieshangai/backup-manifest.tsv
//   line 1: "# jieshangai backup manifest v1"
//   then one line per touched user file, sorted by path (bytewise):
//   <path relative to root>\t<had_original 0|1>\t<sha256 of backup|->\t<sha256 of our last write|->
//
// First write of each file: copy original to <file>.jieshangai.bak (an existing
// .bak from an older /i is adopted) and record its fingerprint. Each write
// records the fingerprint of what we wrote.
//
// Restore (= /u):
//   1. any tracked file whose current hash != our last write -> user edited
//      after our write -> ErrNeedsConfirm unless Confirm; nothing changed.
//   2. strip our entries everywhere (Clear).
//   3. file with backup whose fingerprint matches -> copied back verbatim,
//      .bak removed (UsedBackup). Otherwise only our entries are removed; a file
//      that did not exist before and is now empty is deleted; a .bak whose
//      fingerprint does not match is left untouched.
//   4. manifest removed, ~/.jieshangai removed if empty.

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	manifestName   = "backup-manifest.tsv"
	manifestHeader = "# jieshangai backup manifest v1"

	// NoBackupMessage is the exact copy /u prints when no backup was used.
	NoBackupMessage = "已去掉我们加的设置。如果你以前自己选过模型，需要重新选一次。"
	// RestoredMessage is what /u prints when at least one backup was used
	// (CLIENT-ZERO-BASE-COPY ③ 完成).
	RestoredMessage = "已经恢复好了。"
	// EditedNotice is the client's restore-edited line (CLIENT-ZERO-BASE-COPY ③,
	// restore-edited.png), verbatim.
	EditedNotice = "你后来自己改过的地方，也会一起恢复。"
	// EditedConfirmPrompt is the one line /u prints before asking to continue.
	EditedConfirmPrompt = EditedNotice + "要恢复请输入 y 后按回车："
	// DeclinedMessage is printed when the user does not confirm.
	DeclinedMessage = "没有动任何文件。"
)

type BackupEntry struct {
	HadOriginal bool
	Original    string // fingerprint of the backed-up original
	Written     string // fingerprint of our last write
}

type manifest struct {
	path  string
	root  string
	Files map[string]*BackupEntry // key: path relative to Root
}

func (p Paths) manifestPath() string { return filepath.Join(p.Dir(), manifestName) }

func (p Paths) loadManifest() (*manifest, error) {
	m := &manifest{path: p.manifestPath(), root: p.Root, Files: map[string]*BackupEntry{}}
	raw, err := os.ReadFile(m.path)
	if os.IsNotExist(err) {
		return m, nil
	}
	if err != nil {
		return nil, err
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.Split(line, "\t")
		if len(f) != 4 {
			return nil, ErrConfigUnreadable
		}
		dash := func(s string) string {
			if s == "-" {
				return ""
			}
			return s
		}
		m.Files[f[0]] = &BackupEntry{HadOriginal: f[1] == "1", Original: dash(f[2]), Written: dash(f[3])}
	}
	return m, nil
}

func (m *manifest) keys() []string {
	ks := make([]string, 0, len(m.Files))
	for k := range m.Files {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

func (m *manifest) key(path string) string {
	rel, err := filepath.Rel(m.root, path)
	if err != nil {
		return path
	}
	return filepath.ToSlash(rel)
}

func (m *manifest) abs(key string) string {
	if filepath.IsAbs(key) {
		return key
	}
	return filepath.Join(m.root, filepath.FromSlash(key))
}

func (m *manifest) save() error {
	if len(m.Files) == 0 {
		if err := os.Remove(m.path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(m.path), 0o700); err != nil {
		return err
	}
	var b strings.Builder
	b.WriteString(manifestHeader + "\n")
	dash := func(s string) string {
		if s == "" {
			return "-"
		}
		return s
	}
	for _, k := range m.keys() {
		e := m.Files[k]
		had := "0"
		if e.HadOriginal {
			had = "1"
		}
		b.WriteString(k + "\t" + had + "\t" + dash(e.Original) + "\t" + dash(e.Written) + "\n")
	}
	return atomicWrite(m.path, []byte(b.String()), 0o600)
}

// backupOnce mirrors backup_once in install.sh.
func (m *manifest) backupOnce(path string) error {
	k := m.key(path)
	if _, ok := m.Files[k]; ok {
		return nil
	}
	bak := path + BackupSuffix
	e := &BackupEntry{}
	if st, err := os.Stat(bak); err == nil && st.Mode().IsRegular() {
		h, err := fileHash(bak)
		if err != nil {
			return err
		}
		e.HadOriginal, e.Original = true, h
	} else if st, err := os.Stat(path); err == nil && st.Mode().IsRegular() {
		if err := copyFile(path, bak, st.Mode().Perm()); err != nil {
			return err
		}
		h, err := fileHash(bak)
		if err != nil {
			return err
		}
		e.HadOriginal, e.Original = true, h
	} else if err != nil && !os.IsNotExist(err) {
		return err
	}
	m.Files[k] = e
	return m.save()
}

// recordWrite mirrors record_write in install.sh.
func (m *manifest) recordWrite(path string) error {
	e, ok := m.Files[m.key(path)]
	if !ok {
		return nil
	}
	h, err := fileHash(path)
	if err != nil {
		return err
	}
	e.Written = h
	return m.save()
}

type RestoreOpts struct {
	Confirm bool // required when a tracked file was edited after our write
}

// FileResult is the per-file outcome of Restore.
type FileResult struct {
	Path       string
	UsedBackup bool // true: original copied back from backup; false: only our entries removed
}

type RestoreReport struct {
	UsedBackup bool         // overall: true if at least one file was restored from backup
	Files      []FileResult // one per tracked file, sorted by path
	Edited     []string     // files changed after our write (need/used Confirm)
	Restored   []string     // replaced from backup
	Cleaned    []string     // no usable backup: only our entries removed
}

// Message is the closing line /u prints for this outcome.
func (r RestoreReport) Message() string {
	if r.UsedBackup {
		return RestoredMessage
	}
	return NoBackupMessage
}

// EditedSinceWrite lists tracked files the user changed after our last write.
func (p Paths) EditedSinceWrite() ([]string, error) {
	m, err := p.loadManifest()
	if err != nil {
		return nil, err
	}
	return m.edited(), nil
}

func (m *manifest) edited() []string {
	var out []string
	for _, k := range m.keys() {
		e := m.Files[k]
		if e.Written == "" {
			continue
		}
		h, err := fileHash(m.abs(k))
		if err != nil || h != e.Written {
			out = append(out, m.abs(k))
		}
	}
	return out
}

// Restore is the client's /u. Without Confirm it changes nothing when any
// tracked file was edited after our write.
func (p Paths) Restore(o RestoreOpts) (RestoreReport, error) {
	var r RestoreReport
	m, err := p.loadManifest()
	if err != nil {
		return r, err
	}
	r.Edited = m.edited()
	if len(r.Edited) > 0 && !o.Confirm {
		return r, ErrNeedsConfirm
	}
	if err := p.Clear(); err != nil {
		return r, err
	}
	for _, k := range m.keys() {
		e := m.Files[k]
		path := m.abs(k)
		bak := path + BackupSuffix
		if h, err := fileHash(bak); e.HadOriginal && err == nil && h == e.Original {
			st, err := os.Stat(bak)
			if err != nil {
				return r, err
			}
			if err := copyFile(bak, path, st.Mode().Perm()); err != nil {
				return r, err
			}
			if err := os.Remove(bak); err != nil {
				return r, err
			}
			r.Restored = append(r.Restored, path)
			r.Files = append(r.Files, FileResult{Path: path, UsedBackup: true})
			r.UsedBackup = true
		} else {
			if !e.HadOriginal {
				if err := removeIfLeftEmpty(path); err != nil {
					return r, err
				}
			}
			r.Cleaned = append(r.Cleaned, path)
			r.Files = append(r.Files, FileResult{Path: path})
		}
		delete(m.Files, k)
	}
	if err := m.save(); err != nil {
		return r, err
	}
	_ = os.Remove(p.Dir())
	return r, nil
}

// removeIfLeftEmpty deletes a file we created when nothing but our (now
// stripped) content was in it. Mirrors remove_if_left_empty in uninstall.sh.
func removeIfLeftEmpty(path string) error {
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	switch string(b) {
	case "", "\n", "{}\n":
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

func hashBytes(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }

func fileHash(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func copyFile(src, dst string, mode os.FileMode) error {
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return atomicWrite(dst, b, mode)
}
