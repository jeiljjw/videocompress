package engine

import (
	"os"
	"path/filepath"
	"testing"
)

func write(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestSingleOutputPathNoCollision(t *testing.T) {
	dir := t.TempDir()
	got, err := SingleOutputPath(filepath.Join(dir, "movie.mp4"), "")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(dir, "movie_compressed.mp4")
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestSingleOutputPathCollisions(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "movie_compressed.mp4"))
	got, _ := SingleOutputPath(filepath.Join(dir, "movie.mp4"), "")
	if filepath.Base(got) != "movie_compressed (2).mp4" {
		t.Fatalf("got %q", got)
	}
	write(t, got)
	got, _ = SingleOutputPath(filepath.Join(dir, "movie.mp4"), "")
	if filepath.Base(got) != "movie_compressed (3).mp4" {
		t.Fatalf("got %q", got)
	}
}

func TestSingleOutputPathUsesOutDir(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "out")
	got, err := SingleOutputPath(filepath.Join(dir, "movie.mp4"), out)
	if err != nil {
		t.Fatal(err)
	}
	if got != filepath.Join(out, "movie_compressed.mp4") {
		t.Fatalf("got %q", got)
	}
}

func TestSplitOutputPaths(t *testing.T) {
	dir := t.TempDir()
	got, err := SplitOutputPaths(filepath.Join(dir, "movie.mp4"), "", 3)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		filepath.Join(dir, "movie_part01.mp4"),
		filepath.Join(dir, "movie_part02.mp4"),
		filepath.Join(dir, "movie_part03.mp4"),
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("part %d = %q want %q", i, got[i], want[i])
		}
	}
}

func TestSplitOutputPathsGroupCollision(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "movie_part02.mp4"))
	got, err := SplitOutputPaths(filepath.Join(dir, "movie.mp4"), "", 3)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(got[0]) != "movie (2)_part01.mp4" {
		t.Fatalf("got %q", got[0])
	}
}

func TestSplitOutputPathsWideCount(t *testing.T) {
	dir := t.TempDir()
	got, err := SplitOutputPaths(filepath.Join(dir, "movie.mp4"), "", 120)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(got[0]) != "movie_part001.mp4" || filepath.Base(got[119]) != "movie_part120.mp4" {
		t.Fatalf("first=%q last=%q", filepath.Base(got[0]), filepath.Base(got[119]))
	}
}

func TestMoveFileNoReplace(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.bin")
	dst := filepath.Join(dir, "dst.bin")
	write(t, src)
	if err := MoveFileNoReplace(src, dst); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(src); err == nil {
		t.Fatal("source should be gone")
	}
	write(t, src)
	if err := MoveFileNoReplace(src, dst); err == nil {
		t.Fatal("want error when destination exists")
	}
}
