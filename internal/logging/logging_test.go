package logging

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func TestOpenCreatesDirectoryAndAppends(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "logs")
	l, err := Open(dir)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	l.Printf("hello %s", "world")
	if err := l.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// A second open must append, not truncate.
	l2, err := Open(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	l2.Printf("second run")
	l2.Close()

	b, err := os.ReadFile(filepath.Join(dir, FileName))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	got := string(b)
	if !strings.Contains(got, "hello world") || !strings.Contains(got, "second run") {
		t.Fatalf("log = %q", got)
	}
	if !strings.HasPrefix(got, "20") || !strings.Contains(got, "T") {
		t.Fatalf("lines should be timestamped: %q", got)
	}
}

func TestRotateKeepsAtMostKeepFiles(t *testing.T) {
	dir := t.TempDir()
	l, err := OpenWith(dir, 100, 3)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer l.Close()
	for i := 0; i < 12; i++ {
		l.Printf("line %02d %s", i, strings.Repeat("x", 40))
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	if len(names) != 3 {
		t.Fatalf("kept %d files (%v), want 3", len(names), names)
	}
	if names[0] != FileName || names[1] != FileName+".1" || names[2] != FileName+".2" {
		t.Fatalf("unexpected files: %v", names)
	}
	newest, err := os.ReadFile(filepath.Join(dir, FileName))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(newest), "line 11") {
		t.Fatalf("app.log should hold the newest line: %q", newest)
	}
}

func TestNilLoggerIsSafe(t *testing.T) {
	var l *Logger
	l.Printf("nothing happens")
	if err := l.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
}
