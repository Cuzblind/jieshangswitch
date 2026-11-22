package clientruntime

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func archivePath(root, name string) (string, error) {
	if strings.Contains(name, "\\") || strings.Contains(name, ":") || strings.HasPrefix(name, "/") {
		return "", errors.New("unsafe archive entry")
	}
	p := filepath.Join(root, filepath.FromSlash(name))
	rel, err := filepath.Rel(root, p)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", errors.New("unsafe archive entry")
	}
	return p, nil
}
func writeArchiveFile(path string, mode os.FileMode, r io.Reader, size int64) error {
	if size < 0 || size > 2<<30 {
		return errors.New("archive entry too large")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode.Perm()|0600)
	if err != nil {
		return err
	}
	_, err = io.CopyN(f, r, size)
	ce := f.Close()
	if err != nil {
		return err
	}
	return ce
}
func copyToolFile(source, dest string, mode os.FileMode) error {
	f, e := os.Open(source)
	if e != nil {
		return e
	}
	defer f.Close()
	if e = os.MkdirAll(filepath.Dir(dest), 0700); e != nil {
		return e
	}
	out, e := os.OpenFile(dest, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode.Perm()|0600)
	if e != nil {
		return e
	}
	_, e = io.Copy(out, f)
	closeErr := out.Close()
	if e != nil {
		return e
	}
	return closeErr
}

// ditto is needed to preserve signed macOS bundle metadata. Validate all paths
// and symlink targets before letting it extract an official application ZIP.
func validateGUIZIP(source, root string) error {
	z, err := zip.OpenReader(source)
	if err != nil {
		return err
	}
	defer z.Close()
	if len(z.File) > 200000 {
		return errors.New("desktop archive too large")
	}
	links := map[string]bool{}
	var total uint64
	for _, f := range z.File {
		p, err := archivePath(root, f.Name)
		if err != nil {
			return err
		}
		total += f.UncompressedSize64
		if total > 20<<30 || f.UncompressedSize64 > 8<<30 {
			return errors.New("desktop archive too large")
		}
		if f.Mode()&os.ModeSymlink == 0 {
			continue
		}
		if f.UncompressedSize64 > 4096 {
			return errors.New("invalid desktop symlink")
		}
		r, err := f.Open()
		if err != nil {
			return err
		}
		data, err := io.ReadAll(io.LimitReader(r, 4097))
		r.Close()
		if err != nil {
			return err
		}
		target := string(data)
		if filepath.IsAbs(target) || strings.ContainsAny(target, "\\:\x00") || !pathInside(root, filepath.Join(filepath.Dir(p), filepath.FromSlash(target))) {
			return errors.New("unsafe desktop symlink")
		}
		links[p] = true
	}
	for _, f := range z.File {
		p, _ := archivePath(root, f.Name)
		for parent := filepath.Dir(p); parent != root && pathInside(root, parent); parent = filepath.Dir(parent) {
			if links[parent] {
				return errors.New("archive writes through symlink")
			}
		}
	}
	return nil
}
func extractToolArchive(source, root, format string) error {
	if format == "zip" {
		z, e := zip.OpenReader(source)
		if e != nil {
			return e
		}
		defer z.Close()
		var total uint64
		for i, f := range z.File {
			if i > 30000 || f.Mode()&os.ModeSymlink != 0 {
				return errors.New("unsupported archive entry")
			}
			p, e := archivePath(root, f.Name)
			if e != nil {
				return e
			}
			total += f.UncompressedSize64
			if total > 4<<30 {
				return errors.New("archive too large")
			}
			if f.FileInfo().IsDir() {
				if e = os.MkdirAll(p, 0700); e != nil {
					return e
				}
				continue
			}
			r, e := f.Open()
			if e != nil {
				return e
			}
			e = writeArchiveFile(p, f.Mode(), r, int64(f.UncompressedSize64))
			r.Close()
			if e != nil {
				return e
			}
		}
		return nil
	}
	f, e := os.Open(source)
	if e != nil {
		return e
	}
	defer f.Close()
	gz, e := gzip.NewReader(f)
	if e != nil {
		return e
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	var total int64
	for count := 0; ; count++ {
		h, e := tr.Next()
		if e == io.EOF {
			return nil
		}
		if e != nil {
			return e
		}
		if count > 30000 {
			return errors.New("archive too many entries")
		}
		p, e := archivePath(root, h.Name)
		if e != nil {
			return e
		}
		total += h.Size
		if total > 4<<30 {
			return errors.New("archive too large")
		}
		switch h.Typeflag {
		case tar.TypeDir:
			e = os.MkdirAll(p, 0700)
		case tar.TypeReg, tar.TypeRegA:
			e = writeArchiveFile(p, os.FileMode(h.Mode), tr, h.Size)
		case tar.TypeSymlink:
			// Node's npm/npx links are internal to its own version directory.
			name := filepath.Base(h.Name)
			if name == "corepack" && filepath.Base(filepath.Dir(h.Name)) == "bin" && h.Linkname == "../lib/node_modules/corepack/dist/corepack.js" {
				continue
			}
			if (name != "npm" && name != "npx") || filepath.Base(filepath.Dir(h.Name)) != "bin" || h.Linkname != "../lib/node_modules/npm/bin/"+name+"-cli.js" {
				return errors.New("unsafe symlink")
			}
			if _, e = archivePath(root, filepath.ToSlash(filepath.Join(filepath.Dir(h.Name), h.Linkname))); e != nil {
				return e
			}
			if e = os.MkdirAll(filepath.Dir(p), 0700); e == nil {
				e = os.Symlink(h.Linkname, p)
			}
		default:
			return errors.New("unsupported archive entry")
		}
		if e != nil {
			return e
		}
	}
}
