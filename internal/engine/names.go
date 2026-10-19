package engine

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// SingleOutputPath returns a non-existing `<base>_compressed.mp4` path,
// adding a ` (2)`, ` (3)` suffix on collision.
func SingleOutputPath(inputPath, outDir string) (string, error) {
	dir := outputDir(inputPath, outDir)
	base := baseName(inputPath)
	for i := 1; i <= 9999; i++ {
		name := base + "_compressed.mp4"
		if i > 1 {
			name = fmt.Sprintf("%s_compressed (%d).mp4", base, i)
		}
		p := filepath.Join(dir, name)
		if !exists(p) {
			return p, nil
		}
	}
	return "", fmt.Errorf("출력 파일 이름을 정할 수 없습니다: %s", base)
}

// SplitOutputPaths returns non-existing part paths. If any member of a group
// would collide, the next group number ` (2)`, ` (3)` is used.
func SplitOutputPaths(inputPath, outDir string, count int) ([]string, error) {
	if count < 1 {
		return nil, fmt.Errorf("조각 수가 올바르지 않습니다: %d", count)
	}
	dir := outputDir(inputPath, outDir)
	base := baseName(inputPath)
	digits := 2
	if n := len(strconv.Itoa(count)); n > digits {
		digits = n
	}
	for g := 1; g <= 9999; g++ {
		suffix := ""
		if g > 1 {
			suffix = fmt.Sprintf(" (%d)", g)
		}
		paths := make([]string, 0, count)
		conflict := false
		for i := 1; i <= count; i++ {
			p := filepath.Join(dir, fmt.Sprintf("%s%s_part%0*d.mp4", base, suffix, digits, i))
			if exists(p) {
				conflict = true
				break
			}
			paths = append(paths, p)
		}
		if !conflict {
			return paths, nil
		}
	}
	return nil, fmt.Errorf("출력 파일 이름을 정할 수 없습니다: %s", base)
}

func outputDir(inputPath, outDir string) string {
	if strings.TrimSpace(outDir) != "" {
		return outDir
	}
	return filepath.Dir(inputPath)
}

func baseName(inputPath string) string {
	return strings.TrimSuffix(filepath.Base(inputPath), filepath.Ext(inputPath))
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// MoveFileNoReplace moves src to dst, refusing to overwrite an existing file.
// If rename fails (different volumes), it falls back to copy+remove.
func MoveFileNoReplace(src, dst string) error {
	if exists(dst) {
		return fmt.Errorf("대상 파일이 이미 존재합니다: %s", dst)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	if err := copyExclusive(src, dst); err != nil {
		return err
	}
	return os.Remove(src)
}

func copyExclusive(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(dst)
		return err
	}
	if err := out.Sync(); err != nil {
		out.Close()
		os.Remove(dst)
		return err
	}
	return out.Close()
}
