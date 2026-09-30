// Package dl contains HTTP helpers for downloads with progress reporting.
package dl

import (
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// Progress reports a status message and the progress in percent (-1 = unknown).
type Progress func(msg string, pct float64)

var client = &http.Client{Timeout: 0}

const userAgent = "MasterMC/1.0 (+https://github.com/mastermc)"

func get(ctx context.Context, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("GET %s: HTTP %d", url, resp.StatusCode)
	}
	return resp, nil
}

// JSON fetches a URL and decodes the response into v.
func JSON(ctx context.Context, url string, v any) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	resp, err := get(ctx, url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return json.NewDecoder(resp.Body).Decode(v)
}

// Open opens a download stream that reports progress while being read.
func Open(ctx context.Context, url, label string, p Progress) (io.ReadCloser, error) {
	resp, err := get(ctx, url)
	if err != nil {
		return nil, err
	}
	return &progressReader{rc: resp.Body, total: resp.ContentLength, label: label, p: p}, nil
}

// File downloads url to dest. If algo ("sha1"/"sha256") is set, the checksum
// is verified. The file is only moved into place after success.
func File(ctx context.Context, url, dest, algo, sum string, p Progress) error {
	body, err := Open(ctx, url, "Downloading "+filepath.Base(dest), p)
	if err != nil {
		return err
	}
	defer body.Close()
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	tmp := dest + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	var h hash.Hash
	switch algo {
	case "sha1":
		h = sha1.New()
	case "sha256":
		h = sha256.New()
	}
	w := io.Writer(f)
	if h != nil {
		w = io.MultiWriter(f, h)
	}
	_, err = io.Copy(w, body)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(tmp)
		return err
	}
	if h != nil && sum != "" {
		if got := hex.EncodeToString(h.Sum(nil)); got != sum {
			os.Remove(tmp)
			return fmt.Errorf("checksum mismatch (%s): expected %s, got %s", algo, sum, got)
		}
	}
	return os.Rename(tmp, dest)
}

type progressReader struct {
	rc    io.ReadCloser
	total int64
	done  int64
	label string
	p     Progress
	last  time.Time
}

func (r *progressReader) Read(b []byte) (int, error) {
	n, err := r.rc.Read(b)
	r.done += int64(n)
	if r.p != nil && (time.Since(r.last) > 250*time.Millisecond || err == io.EOF) {
		r.last = time.Now()
		pct := -1.0
		if r.total > 0 {
			pct = float64(r.done) * 100 / float64(r.total)
		}
		r.p(fmt.Sprintf("%s (%.1f MB)", r.label, float64(r.done)/1e6), pct)
	}
	return n, err
}

func (r *progressReader) Close() error { return r.rc.Close() }
