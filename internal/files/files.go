// Package files provides safe file operations inside the server directory.
// All paths are relative to the root; escapes via "..", absolute paths
// or symlinks pointing outside are rejected.
package files

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

var ErrOutside = errors.New("path is outside the server directory")

const MaxEditSize = 5 << 20 // 5 MB

type FS struct {
	Root string
}

type Entry struct {
	Name    string `json:"name"`
	Dir     bool   `json:"dir"`
	Size    int64  `json:"size"`
	ModTime int64  `json:"modTime"`
	Link    bool   `json:"link"`
}

func (f *FS) root() (string, error) {
	if err := os.MkdirAll(f.Root, 0o755); err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(f.Root)
}

func within(root, p string) bool {
	return p == root || strings.HasPrefix(p, root+string(os.PathSeparator))
}

// Resolve converts a relative path into an absolute, validated path.
func (f *FS) Resolve(rel string) (string, error) {
	root, err := f.root()
	if err != nil {
		return "", err
	}
	if strings.ContainsRune(rel, 0) {
		return "", ErrOutside
	}
	p := filepath.Join(root, filepath.Clean("/"+filepath.ToSlash(rel)))
	if !within(root, p) {
		return "", ErrOutside
	}
	// Resolve the deepest existing ancestor and check that symlinks
	// do not lead out of the server directory.
	existing, rest := p, ""
	for {
		if _, err := os.Lstat(existing); err == nil {
			break
		}
		parent := filepath.Dir(existing)
		if parent == existing {
			break
		}
		rest = filepath.Join(filepath.Base(existing), rest)
		existing = parent
	}
	real, err := filepath.EvalSymlinks(existing)
	if err != nil {
		// Broken symlink: only allow Lstat-based operations (e.g. delete).
		if within(root, existing) {
			return p, nil
		}
		return "", ErrOutside
	}
	if !within(root, real) {
		return "", ErrOutside
	}
	return filepath.Join(real, rest), nil
}

// IsRoot reports whether the relative path is the root itself.
func (f *FS) IsRoot(rel string) bool {
	return filepath.Clean("/"+filepath.ToSlash(rel)) == "/"
}

func (f *FS) List(rel string) ([]Entry, error) {
	p, err := f.Resolve(rel)
	if err != nil {
		return nil, err
	}
	des, err := os.ReadDir(p)
	if err != nil {
		return nil, err
	}
	out := make([]Entry, 0, len(des))
	for _, de := range des {
		e := Entry{Name: de.Name(), Link: de.Type()&fs.ModeSymlink != 0}
		info, err := os.Stat(filepath.Join(p, de.Name()))
		if err != nil {
			info, err = de.Info()
			if err != nil {
				continue
			}
		}
		e.Dir = info.IsDir()
		e.Size = info.Size()
		e.ModTime = info.ModTime().Unix()
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Dir != out[j].Dir {
			return out[i].Dir
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out, nil
}

// ReadText reads a file for editing. Binary and oversized files are rejected.
func (f *FS) ReadText(rel string) (string, error) {
	p, err := f.Resolve(rel)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(p)
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		return "", errors.New("is a directory")
	}
	if info.Size() > MaxEditSize {
		return "", fmt.Errorf("file too large to edit (%d MB) – please download it", info.Size()>>20)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return "", err
	}
	if bytes.IndexByte(b, 0) >= 0 || !utf8.Valid(b) {
		return "", errors.New("binary file – cannot be edited as text")
	}
	return string(b), nil
}

// WriteText saves a file atomically and keeps its permissions.
func (f *FS) WriteText(rel, content string) error {
	p, err := f.Resolve(rel)
	if err != nil {
		return err
	}
	mode := os.FileMode(0o644)
	if info, err := os.Stat(p); err == nil {
		if info.IsDir() {
			return errors.New("is a directory")
		}
		mode = info.Mode().Perm()
	} else if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(p), ".mastermc-*")
	if err != nil {
		return err
	}
	_, err = tmp.WriteString(content)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(tmp.Name(), mode)
	}
	if err == nil {
		err = os.Rename(tmp.Name(), p)
	}
	if err != nil {
		os.Remove(tmp.Name())
	}
	return err
}

// Create creates a file for writing (for uploads); missing directories are created.
func (f *FS) Create(rel string) (*os.File, error) {
	if f.IsRoot(rel) {
		return nil, errors.New("invalid file name")
	}
	p, err := f.Resolve(rel)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return nil, err
	}
	// Re-check the path after MkdirAll (symlink race).
	if p2, err := f.Resolve(rel); err != nil || p2 != p {
		return nil, ErrOutside
	}
	return os.Create(p)
}

func (f *FS) Mkdir(rel string) error {
	p, err := f.Resolve(rel)
	if err != nil {
		return err
	}
	return os.MkdirAll(p, 0o755)
}

func (f *FS) Rename(from, to string) error {
	if f.IsRoot(from) || f.IsRoot(to) {
		return errors.New("the server directory itself cannot be renamed")
	}
	src, err := f.Resolve(from)
	if err != nil {
		return err
	}
	dst, err := f.Resolve(to)
	if err != nil {
		return err
	}
	if _, err := os.Lstat(dst); err == nil {
		return errors.New("target already exists")
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	return os.Rename(src, dst)
}

func (f *FS) Delete(rel string) error {
	if f.IsRoot(rel) {
		return errors.New("the server directory itself cannot be deleted")
	}
	// Remove symlinks themselves, not their target. For that only the parent
	// directory has to be inside the server directory.
	clean := filepath.Clean("/" + filepath.ToSlash(rel))
	parent, err := f.Resolve(filepath.Dir(clean))
	if err != nil {
		return err
	}
	lp := filepath.Join(parent, filepath.Base(clean))
	info, err := os.Lstat(lp)
	if err != nil {
		return err
	}
	if info.Mode()&fs.ModeSymlink != 0 {
		return os.Remove(lp)
	}
	p, err := f.Resolve(rel)
	if err != nil {
		return err
	}
	return os.RemoveAll(p)
}

// Clear empties the root directory completely.
func (f *FS) Clear() error {
	root, err := f.root()
	if err != nil {
		return err
	}
	des, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	for _, de := range des {
		if err := os.RemoveAll(filepath.Join(root, de.Name())); err != nil {
			return err
		}
	}
	return nil
}

// Zip writes the given files/directories (relative to base) as a ZIP to w.
func (f *FS) Zip(w io.Writer, base string, names []string) error {
	zw := zip.NewWriter(w)
	for _, name := range names {
		p, err := f.Resolve(filepath.Join(base, name))
		if err != nil {
			return err
		}
		parent := filepath.Dir(p)
		err = filepath.WalkDir(p, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil // skip unreadable files
			}
			rel, _ := filepath.Rel(parent, path)
			rel = filepath.ToSlash(rel)
			if d.Type()&fs.ModeSymlink != 0 {
				return nil
			}
			info, err := d.Info()
			if err != nil {
				return nil
			}
			if d.IsDir() {
				_, err := zw.CreateHeader(&zip.FileHeader{Name: rel + "/", Modified: info.ModTime()})
				return err
			}
			hdr, err := zip.FileInfoHeader(info)
			if err != nil {
				return err
			}
			hdr.Name = rel
			hdr.Method = zip.Deflate
			fw, err := zw.CreateHeader(hdr)
			if err != nil {
				return err
			}
			src, err := os.Open(path)
			if err != nil {
				return nil
			}
			defer src.Close()
			_, err = io.Copy(fw, src)
			return err
		})
		if err != nil {
			return err
		}
	}
	return zw.Close()
}

// Unzip extracts a ZIP archive into the directory destRel.
func (f *FS) Unzip(zipRel, destRel string) error {
	zp, err := f.Resolve(zipRel)
	if err != nil {
		return err
	}
	zr, err := zip.OpenReader(zp)
	if err != nil {
		return err
	}
	defer zr.Close()
	for _, zf := range zr.File {
		target := filepath.Join(destRel, zf.Name)
		if zf.FileInfo().IsDir() {
			if err := f.Mkdir(target); err != nil {
				return err
			}
			continue
		}
		if zf.Mode()&fs.ModeSymlink != 0 {
			continue
		}
		if err := func() error {
			src, err := zf.Open()
			if err != nil {
				return err
			}
			defer src.Close()
			dst, err := f.Create(target)
			if err != nil {
				return err
			}
			defer dst.Close()
			if _, err := io.Copy(dst, src); err != nil {
				return err
			}
			os.Chtimes(dst.Name(), time.Now(), zf.Modified)
			return nil
		}(); err != nil {
			return fmt.Errorf("%s: %w", zf.Name, err)
		}
	}
	return nil
}
