// Package state records running shares, one file per port, so --list and --stop can find them.
// A record is lines of key=value.
package state

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Record is one running share.
type Record struct {
	Port    int
	URL     string
	Adapter string
	Path    string
	PID     int
	Expires string // empty when it never expires
	Target  string // the local server tailscale serve proxies to: http://127.0.0.1:<port>
	Log     string // where a detached share writes its stderr
	Tmp     string // a temporary directory of its own, if it has one

	File string // the record itself
}

// Write records a share in dir, and sets r.File.
func Write(dir string, r *Record) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	r.File = filepath.Join(dir, strconv.Itoa(r.Port))
	text := fmt.Sprintf("port=%d\nurl=%s\nadapter=%s\npath=%s\npid=%d\nexpires=%s\ntarget=%s\nlog=%s\ntmp=%s\n",
		r.Port, r.URL, r.Adapter, r.Path, r.PID, r.Expires, r.Target, r.Log, r.Tmp)
	return os.WriteFile(r.File, []byte(text), 0o644)
}

// Read reads a record.
func Read(file string) (Record, error) {
	f, err := os.Open(file)
	if err != nil {
		return Record{}, err
	}
	defer f.Close()
	r := Record{File: file}
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		key, value, _ := strings.Cut(scanner.Text(), "=")
		switch key {
		case "port":
			r.Port, _ = strconv.Atoi(value)
		case "url":
			r.URL = value
		case "adapter":
			r.Adapter = value
		case "path":
			r.Path = value
		case "pid":
			r.PID, _ = strconv.Atoi(value)
		case "expires":
			r.Expires = value
		case "target":
			r.Target = value
		case "log":
			r.Log = value
		case "tmp":
			r.Tmp = value
		}
	}
	return r, scanner.Err()
}

// All returns the records in dir, by port. Files that are not records (logs) are left out.
func All(dir string) []Record {
	entries, _ := os.ReadDir(dir)
	var records []Record
	for _, entry := range entries {
		if _, err := strconv.Atoi(entry.Name()); err != nil || entry.IsDir() {
			continue
		}
		if r, err := Read(filepath.Join(dir, entry.Name())); err == nil {
			records = append(records, r)
		}
	}
	sort.Slice(records, func(i, j int) bool { return records[i].Port < records[j].Port })
	return records
}

// Remove removes the record and what the share left: its log and its temporary directory.
func (r Record) Remove() {
	if r.Log != "" {
		os.Remove(r.Log)
	}
	if r.Tmp != "" {
		os.RemoveAll(r.Tmp)
	}
	if r.File != "" {
		os.Remove(r.File)
	}
}
