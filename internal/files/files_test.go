package files

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveSandbox(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "server")
	outside := filepath.Join(base, "secret")
	os.MkdirAll(root, 0o755)
	os.MkdirAll(outside, 0o755)
	os.WriteFile(filepath.Join(outside, "pw.txt"), []byte("x"), 0o644)
	os.Symlink(outside, filepath.Join(root, "evil"))
	os.Symlink("/etc/passwd", filepath.Join(root, "passwd"))
	os.MkdirAll(filepath.Join(root, "world"), 0o755)

	f := &FS{Root: root}
	realRoot, _ := filepath.EvalSymlinks(root)

	ok := map[string]string{
		"":                 realRoot,
		"/":                realRoot,
		"world":            filepath.Join(realRoot, "world"),
		"world/../world/x": filepath.Join(realRoot, "world", "x"),
		"../../etc/passwd": filepath.Join(realRoot, "etc", "passwd"), // forced into the root
		"/etc/passwd":      filepath.Join(realRoot, "etc", "passwd"),
		"new/dir/file.txt": filepath.Join(realRoot, "new", "dir", "file.txt"),
	}
	for in, want := range ok {
		got, err := f.Resolve(in)
		if err != nil || got != want {
			t.Errorf("Resolve(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"evil", "evil/pw.txt", "evil/new.txt", "passwd"} {
		if got, err := f.Resolve(in); err == nil {
			t.Errorf("Resolve(%q) = %q; expected error", in, got)
		}
	}
	if err := f.Delete("evil"); err != nil {
		t.Fatalf("deleting symlink: %v", err)
	}
	if _, err := os.Stat(filepath.Join(outside, "pw.txt")); err != nil {
		t.Fatal("symlink target was deleted!")
	}
	if err := f.Delete(""); err == nil {
		t.Error("deleting root should fail")
	}
}
