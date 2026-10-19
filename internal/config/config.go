package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Settings holds user-configurable limits. Sizes are decimal megabytes.
type Settings struct {
	TargetMB   int    `json:"targetMB"`
	PartMB     int    `json:"partMB"`
	FFmpegPath string `json:"ffmpegPath,omitempty"`
}

// Default returns the built-in defaults (5MB target, 4MB parts).
func Default() Settings {
	return Settings{TargetMB: 5, PartMB: 4}
}

// Validate enforces 2 <= TargetMB <= 100 and 1 <= PartMB < TargetMB.
func (s Settings) Validate() error {
	if s.TargetMB < 2 || s.TargetMB > 100 {
		return fmt.Errorf("목표 크기는 2~100MB여야 합니다 (현재 %dMB)", s.TargetMB)
	}
	if s.PartMB < 1 || s.PartMB >= s.TargetMB {
		return fmt.Errorf("조각 크기는 1MB 이상, 목표 크기 미만이어야 합니다 (현재 %dMB)", s.PartMB)
	}
	return nil
}

func (s Settings) TargetBytes() int64 { return int64(s.TargetMB) * 1_000_000 }
func (s Settings) PartBytes() int64   { return int64(s.PartMB) * 1_000_000 }

// Load reads settings from path. A missing file yields defaults. Any read,
// parse, or validation error also yields defaults together with the error so
// callers can start in a safe state.
func Load(path string) (Settings, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Default(), nil
		}
		return Default(), err
	}
	s := Default()
	if err := json.Unmarshal(data, &s); err != nil {
		return Default(), fmt.Errorf("설정 파일을 읽을 수 없습니다: %w", err)
	}
	if err := s.Validate(); err != nil {
		return Default(), err
	}
	return s, nil
}

// Save validates and atomically writes settings to path.
func Save(path string, s Settings) error {
	if err := s.Validate(); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".settings-*.json")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

// writeFile is a small helper used by tests.
func writeFile(path string, data []byte) error {
	return os.WriteFile(path, data, 0o644)
}
