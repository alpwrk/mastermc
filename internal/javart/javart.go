// Package javart finds, installs and removes Java runtimes.
package javart

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"mastermc/internal/dl"
)

type Runtime struct {
	Path    string `json:"path"`
	Major   int    `json:"major"`
	Version string `json:"version"`
	Managed bool   `json:"managed"` // installed by the panel (deletable)
}

type Manager struct {
	Dir string // <data>/java
}

var versionRe = regexp.MustCompile(`version "([^"]+)"`)

// Probe runs `java -version` and parses the version.
func Probe(path string) (Runtime, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, "-version").CombinedOutput()
	if err != nil {
		return Runtime{}, fmt.Errorf("%s -version: %v", path, err)
	}
	m := versionRe.FindSubmatch(out)
	if m == nil {
		return Runtime{}, fmt.Errorf("unknown Java version: %s", strings.TrimSpace(string(out)))
	}
	v := string(m[1])
	return Runtime{Path: path, Version: v, Major: majorOf(v)}, nil
}

func majorOf(v string) int {
	v = strings.TrimPrefix(v, "1.") // 1.8.0_392 -> 8
	end := strings.IndexFunc(v, func(r rune) bool { return r < '0' || r > '9' })
	if end >= 0 {
		v = v[:end]
	}
	n, _ := strconv.Atoi(v)
	return n
}

// List returns all Java runtimes installed by the panel or found on the system.
func (m *Manager) List() []Runtime {
	var out []Runtime
	seen := map[string]bool{}
	add := func(path string, managed bool) {
		real, err := filepath.EvalSymlinks(path)
		if err != nil || seen[real] {
			return
		}
		seen[real] = true
		rt, err := Probe(path)
		if err != nil {
			return
		}
		rt.Managed = managed
		out = append(out, rt)
	}
	if entries, err := os.ReadDir(m.Dir); err == nil {
		for _, e := range entries {
			if e.IsDir() {
				add(filepath.Join(m.Dir, e.Name(), "bin", "java"), true)
			}
		}
	}
	if p, err := exec.LookPath("java"); err == nil {
		add(p, false)
	}
	for _, pattern := range []string{"/usr/lib/jvm/*/bin/java", "/usr/lib64/jvm/*/bin/java", "/opt/java/*/bin/java", "/opt/*jdk*/bin/java"} {
		matches, _ := filepath.Glob(pattern)
		for _, p := range matches {
			add(p, false)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Major != out[j].Major {
			return out[i].Major > out[j].Major
		}
		return out[i].Managed && !out[j].Managed
	})
	return out
}

// Find looks for an installed Java with exactly this major version.
func (m *Manager) Find(major int) (Runtime, bool) {
	for _, rt := range m.List() {
		if rt.Major == major {
			return rt, true
		}
	}
	return Runtime{}, false
}

type Available struct {
	Releases []int `json:"releases"`
	LTS      []int `json:"lts"`
	Latest   int   `json:"latest"`
}

func (m *Manager) Available(ctx context.Context) (Available, error) {
	var r struct {
		AvailableLTS      []int `json:"available_lts_releases"`
		AvailableReleases []int `json:"available_releases"`
		MostRecentLTS     int   `json:"most_recent_lts"`
	}
	if err := dl.JSON(ctx, "https://api.adoptium.net/v3/info/available_releases", &r); err != nil {
		return Available{}, err
	}
	return Available{Releases: r.AvailableReleases, LTS: r.AvailableLTS, Latest: r.MostRecentLTS}, nil
}

func adoptiumArch() string {
	switch runtime.GOARCH {
	case "amd64":
		return "x64"
	case "arm64":
		return "aarch64"
	case "386":
		return "x32"
	default:
		return runtime.GOARCH // arm, ppc64le, s390x, riscv64
	}
}

func adoptiumOS() string {
	if _, err := os.Stat("/etc/alpine-release"); err == nil {
		return "alpine-linux"
	}
	return "linux"
}

// Install downloads Temurin (jre or jdk) and extracts it to <Dir>/<major>.
// If no JRE exists for the platform, the JDK is used automatically.
func (m *Manager) Install(ctx context.Context, major int, imageType string, p dl.Progress) (Runtime, error) {
	if major < 8 || major > 99 {
		return Runtime{}, fmt.Errorf("invalid Java version %d", major)
	}
	if imageType != "jdk" {
		imageType = "jre"
	}
	url := func(img string) string {
		return fmt.Sprintf("https://api.adoptium.net/v3/binary/latest/%d/ga/%s/%s/%s/hotspot/normal/eclipse",
			major, adoptiumOS(), adoptiumArch(), img)
	}
	p(fmt.Sprintf("Looking up Java %d (%s) for %s/%s", major, imageType, adoptiumOS(), adoptiumArch()), -1)
	body, err := dl.Open(ctx, url(imageType), fmt.Sprintf("Downloading Java %d", major), p)
	if err != nil && imageType == "jre" {
		p("No JRE available, trying JDK", -1)
		body, err = dl.Open(ctx, url("jdk"), fmt.Sprintf("Downloading Java %d", major), p)
	}
	if err != nil {
		return Runtime{}, err
	}
	defer body.Close()

	if err := os.MkdirAll(m.Dir, 0o755); err != nil {
		return Runtime{}, err
	}
	tmp, err := os.MkdirTemp(m.Dir, ".install-")
	if err != nil {
		return Runtime{}, err
	}
	defer os.RemoveAll(tmp)
	if err := extractTarGz(body, tmp, 1); err != nil {
		return Runtime{}, fmt.Errorf("extracting: %w", err)
	}
	p("Verifying installation", 100)
	if _, err := Probe(filepath.Join(tmp, "bin", "java")); err != nil {
		return Runtime{}, err
	}
	dest := filepath.Join(m.Dir, strconv.Itoa(major))
	if err := os.RemoveAll(dest); err != nil {
		return Runtime{}, err
	}
	if err := os.Rename(tmp, dest); err != nil {
		return Runtime{}, err
	}
	rt, err := Probe(filepath.Join(dest, "bin", "java"))
	rt.Managed = true
	return rt, err
}

// Delete removes a Java version installed by the panel.
func (m *Manager) Delete(javaPath string) error {
	rel, err := filepath.Rel(m.Dir, javaPath)
	if err != nil || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		return errors.New("only Java versions installed by the panel can be deleted")
	}
	top := strings.Split(filepath.ToSlash(rel), "/")[0]
	if top == "" || top == "." || strings.HasPrefix(top, ".") {
		return errors.New("invalid path")
	}
	return os.RemoveAll(filepath.Join(m.Dir, top))
}

// extractTarGz extracts a tar.gz into dest, stripping the first strip path components.
func extractTarGz(r io.Reader, dest string, strip int) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		clean := strings.Trim(strings.TrimPrefix(filepath.ToSlash(h.Name), "./"), "/")
		parts := strings.Split(clean, "/")
		if len(parts) <= strip {
			continue
		}
		name := filepath.Join(parts[strip:]...)
		target := filepath.Join(dest, name)
		if !strings.HasPrefix(target, filepath.Clean(dest)+string(os.PathSeparator)) {
			return fmt.Errorf("unsafe path in archive: %s", h.Name)
		}
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(h.Mode)&0o777|0o200)
			if err != nil {
				return err
			}
			_, err = io.Copy(f, tr)
			f.Close()
			if err != nil {
				return err
			}
		case tar.TypeSymlink:
			if filepath.IsAbs(h.Linkname) {
				continue
			}
			resolved := filepath.Join(filepath.Dir(target), h.Linkname)
			if !strings.HasPrefix(resolved, filepath.Clean(dest)+string(os.PathSeparator)) {
				continue
			}
			os.MkdirAll(filepath.Dir(target), 0o755)
			if err := os.Symlink(h.Linkname, target); err != nil {
				return err
			}
		}
	}
}
