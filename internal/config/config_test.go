package config

import (
	"path/filepath"
	"testing"
)

func TestDefaultIsValid(t *testing.T) {
	s := Default()
	if err := s.Validate(); err != nil {
		t.Fatalf("default settings invalid: %v", err)
	}
	if s.TargetMB != 5 || s.PartMB != 4 {
		t.Fatalf("unexpected defaults: %+v", s)
	}
}

func TestValidate(t *testing.T) {
	cases := []struct {
		name string
		s    Settings
		ok   bool
	}{
		{"default", Settings{TargetMB: 5, PartMB: 4}, true},
		{"target too small", Settings{TargetMB: 1, PartMB: 1}, false},
		{"part equals target", Settings{TargetMB: 5, PartMB: 5}, false},
		{"part greater", Settings{TargetMB: 5, PartMB: 6}, false},
		{"part zero", Settings{TargetMB: 5, PartMB: 0}, false},
		{"target too big", Settings{TargetMB: 101, PartMB: 4}, false},
		{"minimal", Settings{TargetMB: 2, PartMB: 1}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.s.Validate()
			if tc.ok && err != nil {
				t.Fatalf("want ok, got %v", err)
			}
			if !tc.ok && err == nil {
				t.Fatal("want error, got nil")
			}
		})
	}
}

func TestByteConversions(t *testing.T) {
	s := Settings{TargetMB: 5, PartMB: 4}
	if s.TargetBytes() != 5_000_000 {
		t.Fatalf("target bytes = %d", s.TargetBytes())
	}
	if s.PartBytes() != 4_000_000 {
		t.Fatalf("part bytes = %d", s.PartBytes())
	}
}

func TestLoadMissingReturnsDefault(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.json")
	s, err := Load(path)
	if err != nil {
		t.Fatalf("load missing: %v", err)
	}
	if s != Default() {
		t.Fatalf("want default, got %+v", s)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	want := Settings{TargetMB: 7, PartMB: 3, FFmpegPath: `C:\tools\ffmpeg.exe`}
	if err := Save(path, want); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got != want {
		t.Fatalf("round trip mismatch: got %+v want %+v", got, want)
	}
}

func TestLoadInvalidFallsBackToDefault(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.json")
	if err := writeFile(path, []byte("{not json")); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err == nil {
		t.Fatal("want error for invalid json")
	}
	if got != Default() {
		t.Fatalf("want default fallback, got %+v", got)
	}
}

func TestLoadInvalidValuesFallsBackToDefault(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad-values.json")
	if err := writeFile(path, []byte(`{"targetMB":3,"partMB":9}`)); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err == nil {
		t.Fatal("want validation error")
	}
	if got != Default() {
		t.Fatalf("want default fallback, got %+v", got)
	}
}
