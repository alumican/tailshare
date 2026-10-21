// Package site holds what a share serves, as an fs.FS for http.FileServerFS.
//
// Nothing is copied: Files maps paths to files on disk (opened when they are requested, so they
// follow live) or to bytes made for the share, and Dir serves a directory without its hidden entries.
package site

import (
	"bytes"
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"sort"
	"strings"
	"time"
)

// Entry is one file of a site: a file on disk, or Data when Path is empty.
type Entry struct {
	Path string
	Data []byte
}

// Files is a site made of single files, keyed by slash-separated paths without a leading slash
// ("index.html", "css/style.css"). Directories exist wherever a path goes through them.
type Files map[string]Entry

var created = time.Now()

func (f Files) Open(name string) (fs.File, error) {
	if !fs.ValidPath(name) {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrInvalid}
	}
	if entry, ok := f[name]; ok {
		if entry.Path != "" {
			file, err := os.Open(entry.Path)
			if err != nil {
				return nil, err
			}
			return diskFile{file, path.Base(name)}, nil
		}
		return &memoryFile{Reader: bytes.NewReader(entry.Data), info: info{name: path.Base(name), size: int64(len(entry.Data))}}, nil
	}
	children := f.children(name)
	if children == nil {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
	}
	return &directory{info: info{name: path.Base(name), dir: true}, entries: entries{list: children}}, nil
}

// The entries directly inside a directory, or nil when nothing is inside it
func (f Files) children(dir string) []fs.DirEntry {
	prefix := dir + "/"
	if dir == "." {
		prefix = ""
	}
	seen := map[string]bool{}
	var entries []fs.DirEntry
	for name := range f {
		if !strings.HasPrefix(name, prefix) {
			continue
		}
		child, _, nested := strings.Cut(strings.TrimPrefix(name, prefix), "/")
		if seen[child] {
			continue
		}
		seen[child] = true
		if nested {
			entries = append(entries, fs.FileInfoToDirEntry(info{name: child, dir: true}))
		} else if stat, err := fs.Stat(f, path.Join(dir, child)); err == nil {
			entries = append(entries, fs.FileInfoToDirEntry(stat))
		}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	return entries
}

// Dir serves a directory, leaving out the hidden entries at its top (.git, .env, ...).
// What is inside follows the directory live.
func Dir(dir string) fs.FS {
	return hideTop{os.DirFS(dir)}
}

type hideTop struct{ fsys fs.FS }

func (h hideTop) Open(name string) (fs.File, error) {
	if top, _, _ := strings.Cut(name, "/"); name != "." && strings.HasPrefix(top, ".") {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
	}
	file, err := h.fsys.Open(name)
	if err != nil || name != "." {
		return file, err
	}
	return &hideTopFile{ReadDirFile: file.(fs.ReadDirFile)}, nil
}

type hideTopFile struct {
	fs.ReadDirFile
	entries *entries
}

func (h *hideTopFile) ReadDir(n int) ([]fs.DirEntry, error) {
	if h.entries == nil {
		all, err := h.ReadDirFile.ReadDir(-1)
		if err != nil {
			return nil, err
		}
		visible := all[:0]
		for _, entry := range all {
			if !strings.HasPrefix(entry.Name(), ".") {
				visible = append(visible, entry)
			}
		}
		h.entries = &entries{list: visible}
	}
	return h.entries.next(n)
}

// Directory entries handed out n at a time, as fs.ReadDirFile.ReadDir does
type entries struct{ list []fs.DirEntry }

func (e *entries) next(n int) ([]fs.DirEntry, error) {
	if n <= 0 {
		list := e.list
		e.list = nil
		return list, nil
	}
	if len(e.list) == 0 {
		return nil, io.EOF
	}
	n = min(n, len(e.list))
	list := e.list[:n]
	e.list = e.list[n:]
	return list, nil
}

// A file on disk, named as the site names it rather than as it is named on disk
type diskFile struct {
	*os.File
	name string
}

func (d diskFile) Stat() (fs.FileInfo, error) {
	stat, err := d.File.Stat()
	if err != nil {
		return nil, err
	}
	return renamed{stat, d.name}, nil
}

type renamed struct {
	fs.FileInfo
	name string
}

func (r renamed) Name() string { return r.name }

// MARK: - Files made in memory

type info struct {
	name string
	size int64
	dir  bool
}

func (i info) Name() string       { return i.name }
func (i info) Size() int64        { return i.size }
func (i info) ModTime() time.Time { return created }
func (i info) IsDir() bool        { return i.dir }
func (i info) Sys() any           { return nil }
func (i info) Mode() fs.FileMode {
	if i.dir {
		return fs.ModeDir | 0o555
	}
	return 0o444
}

type memoryFile struct {
	*bytes.Reader
	info info
}

func (m *memoryFile) Stat() (fs.FileInfo, error) { return m.info, nil }
func (m *memoryFile) Close() error               { return nil }

type directory struct {
	info    info
	entries entries
}

func (d *directory) Stat() (fs.FileInfo, error) { return d.info, nil }
func (d *directory) Close() error               { return nil }
func (d *directory) Read([]byte) (int, error) {
	return 0, &fs.PathError{Op: "read", Path: d.info.name, Err: errors.New("is a directory")}
}

func (d *directory) ReadDir(n int) ([]fs.DirEntry, error) {
	return d.entries.next(n)
}
