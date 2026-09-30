// Package installer downloads Minecraft server jars (Vanilla, Paper or any URL).
package installer

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"mastermc/internal/dl"
)

const mojangManifest = "https://piston-meta.mojang.com/mc/game/version_manifest_v2.json"

type Version struct {
	ID   string `json:"id"`
	Type string `json:"type"` // release | snapshot | ...
}

type manifest struct {
	Latest struct {
		Release  string `json:"release"`
		Snapshot string `json:"snapshot"`
	} `json:"latest"`
	Versions []struct {
		ID   string `json:"id"`
		Type string `json:"type"`
		URL  string `json:"url"`
	} `json:"versions"`
}

type cache struct {
	mu   sync.Mutex
	at   time.Time
	data *manifest
}

var mc cache

func loadManifest(ctx context.Context) (*manifest, error) {
	mc.mu.Lock()
	defer mc.mu.Unlock()
	if mc.data != nil && time.Since(mc.at) < 10*time.Minute {
		return mc.data, nil
	}
	var m manifest
	if err := dl.JSON(ctx, mojangManifest, &m); err != nil {
		return nil, err
	}
	mc.data, mc.at = &m, time.Now()
	return &m, nil
}

// VanillaVersions returns all Vanilla versions (newest first).
func VanillaVersions(ctx context.Context, snapshots bool) ([]Version, error) {
	m, err := loadManifest(ctx)
	if err != nil {
		return nil, err
	}
	var out []Version
	for _, v := range m.Versions {
		if v.Type == "release" || (snapshots && v.Type == "snapshot") {
			out = append(out, Version{ID: v.ID, Type: v.Type})
		}
	}
	return out, nil
}

type versionJSON struct {
	Downloads struct {
		Server *struct {
			SHA1 string `json:"sha1"`
			URL  string `json:"url"`
		} `json:"server"`
	} `json:"downloads"`
	JavaVersion struct {
		MajorVersion int `json:"majorVersion"`
	} `json:"javaVersion"`
}

func vanillaMeta(ctx context.Context, id string) (*versionJSON, error) {
	m, err := loadManifest(ctx)
	if err != nil {
		return nil, err
	}
	for _, v := range m.Versions {
		if v.ID == id {
			var vj versionJSON
			if err := dl.JSON(ctx, v.URL, &vj); err != nil {
				return nil, err
			}
			return &vj, nil
		}
	}
	return nil, fmt.Errorf("version %q not found", id)
}

// JavaFor determines the required Java version for a Minecraft version.
func JavaFor(ctx context.Context, mcVersion string) int {
	if vj, err := vanillaMeta(ctx, mcVersion); err == nil && vj.JavaVersion.MajorVersion > 0 {
		return vj.JavaVersion.MajorVersion
	}
	n := parseVersion(mcVersion)
	switch {
	case len(n) >= 1 && n[0] >= 26:
		return 25
	case cmpInts(n, []int{1, 20, 5}) >= 0:
		return 21
	case cmpInts(n, []int{1, 18}) >= 0:
		return 17
	case cmpInts(n, []int{1, 17}) >= 0:
		return 16
	default:
		return 8
	}
}

const paperAPI = "https://fill.papermc.io/v3/projects/paper"

// PaperVersions returns all stable Paper versions (newest first).
func PaperVersions(ctx context.Context) ([]Version, error) {
	var r struct {
		Versions map[string][]string `json:"versions"`
	}
	if err := dl.JSON(ctx, paperAPI, &r); err != nil {
		return nil, err
	}
	var ids []string
	for _, group := range r.Versions {
		for _, v := range group {
			if !strings.ContainsAny(v, "-") { // no pre/rc
				ids = append(ids, v)
			}
		}
	}
	sort.Slice(ids, func(i, j int) bool {
		return cmpInts(parseVersion(ids[i]), parseVersion(ids[j])) > 0
	})
	out := make([]Version, len(ids))
	for i, id := range ids {
		out[i] = Version{ID: id, Type: "release"}
	}
	return out, nil
}

func parseVersion(v string) []int {
	var out []int
	for _, p := range strings.FieldsFunc(v, func(r rune) bool { return r < '0' || r > '9' }) {
		n, _ := strconv.Atoi(p)
		out = append(out, n)
	}
	return out
}

func cmpInts(a, b []int) int {
	for i := 0; i < len(a) || i < len(b); i++ {
		var x, y int
		if i < len(a) {
			x = a[i]
		}
		if i < len(b) {
			y = b[i]
		}
		if x != y {
			if x < y {
				return -1
			}
			return 1
		}
	}
	return 0
}

type Request struct {
	Type        string `json:"type"` // vanilla | paper | custom
	Version     string `json:"version"`
	URL         string `json:"url"`
	JavaVersion int    `json:"javaVersion"` // custom only; 0 = default
	EULA        bool   `json:"eula"`
	Wipe        bool   `json:"wipe"`
}

type Result struct {
	JarName     string
	MCVersion   string
	JavaVersion int
}

// Download fetches the server jar to dir/server.jar.
func Download(ctx context.Context, dir string, req Request, p dl.Progress) (Result, error) {
	jar := filepath.Join(dir, "server.jar")
	res := Result{JarName: "server.jar", MCVersion: req.Version}
	switch req.Type {
	case "vanilla":
		p("Fetching version info for Vanilla "+req.Version, -1)
		vj, err := vanillaMeta(ctx, req.Version)
		if err != nil {
			return res, err
		}
		if vj.Downloads.Server == nil {
			return res, fmt.Errorf("there is no server jar for %s", req.Version)
		}
		res.JavaVersion = vj.JavaVersion.MajorVersion
		if res.JavaVersion == 0 {
			res.JavaVersion = 8
		}
		return res, dl.File(ctx, vj.Downloads.Server.URL, jar, "sha1", vj.Downloads.Server.SHA1, p)

	case "paper":
		p("Looking up latest Paper build for "+req.Version, -1)
		var b struct {
			ID        int `json:"id"`
			Downloads map[string]struct {
				Name      string            `json:"name"`
				Checksums map[string]string `json:"checksums"`
				URL       string            `json:"url"`
			} `json:"downloads"`
		}
		if err := dl.JSON(ctx, paperAPI+"/versions/"+url.PathEscape(req.Version)+"/builds/latest", &b); err != nil {
			return res, err
		}
		d, ok := b.Downloads["server:default"]
		if !ok {
			return res, errors.New("Paper build contains no server download")
		}
		p(fmt.Sprintf("Paper %s Build #%d", req.Version, b.ID), -1)
		res.JavaVersion = JavaFor(ctx, req.Version)
		return res, dl.File(ctx, d.URL, jar, "sha256", d.Checksums["sha256"], p)

	case "custom":
		u, err := url.Parse(strings.TrimSpace(req.URL))
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
			return res, errors.New("invalid URL (http/https required)")
		}
		res.MCVersion = ""
		res.JavaVersion = req.JavaVersion
		if res.JavaVersion == 0 {
			res.JavaVersion = 21
		}
		return res, dl.File(ctx, u.String(), jar, "", "", p)
	}
	return res, fmt.Errorf("unknown type %q", req.Type)
}

// WriteEULA writes eula.txt with eula=true.
func WriteEULA(dir string) error {
	content := "# Accepted via MasterMC (https://aka.ms/MinecraftEULA)\n# " +
		time.Now().Format(time.RFC1123) + "\neula=true\n"
	return os.WriteFile(filepath.Join(dir, "eula.txt"), []byte(content), 0o644)
}
