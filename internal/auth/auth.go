// Package auth handles password hashing, sessions and login protection.
package auth

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	CookieName = "mastermc_session"
	sessionTTL = 7 * 24 * time.Hour
	iterations = 210000
)

func HashPassword(pw string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key, err := pbkdf2.Key(sha256.New, pw, salt, iterations, 32)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("pbkdf2$sha256$%d$%s$%s", iterations,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key)), nil
}

func CheckPassword(hash, pw string) bool {
	parts := strings.Split(hash, "$")
	if len(parts) != 5 || parts[0] != "pbkdf2" || parts[1] != "sha256" {
		return false
	}
	iter, err := strconv.Atoi(parts[2])
	if err != nil {
		return false
	}
	salt, err1 := base64.RawStdEncoding.DecodeString(parts[3])
	want, err2 := base64.RawStdEncoding.DecodeString(parts[4])
	if err1 != nil || err2 != nil {
		return false
	}
	got, err := pbkdf2.Key(sha256.New, pw, salt, iter, len(want))
	if err != nil {
		return false
	}
	return subtle.ConstantTimeCompare(got, want) == 1
}

func RandomToken(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// RandomPassword generates an easily readable random password.
func RandomPassword() string {
	const chars = "abcdefghjkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	b := make([]byte, 16)
	rand.Read(b)
	for i := range b {
		b[i] = chars[int(b[i])%len(chars)]
	}
	return string(b)
}

type Manager struct {
	mu       sync.Mutex
	sessions map[string]time.Time
	fails    map[string][]time.Time
}

func NewManager() *Manager {
	return &Manager{sessions: map[string]time.Time{}, fails: map[string][]time.Time{}}
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// Allowed checks the rate limit: max. 10 failed attempts per IP in 10 minutes.
func (m *Manager) Allowed(r *http.Request) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	ip := clientIP(r)
	cutoff := time.Now().Add(-10 * time.Minute)
	var recent []time.Time
	for _, t := range m.fails[ip] {
		if t.After(cutoff) {
			recent = append(recent, t)
		}
	}
	m.fails[ip] = recent
	return len(recent) < 10
}

func (m *Manager) RecordFail(r *http.Request) {
	m.mu.Lock()
	m.fails[clientIP(r)] = append(m.fails[clientIP(r)], time.Now())
	m.mu.Unlock()
}

func (m *Manager) CreateSession(w http.ResponseWriter, r *http.Request) {
	tok := RandomToken(32)
	m.mu.Lock()
	m.sessions[tok] = time.Now().Add(sessionTTL)
	delete(m.fails, clientIP(r))
	m.mu.Unlock()
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    tok,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		Secure:   r.TLS != nil,
		MaxAge:   int(sessionTTL.Seconds()),
	})
}

func (m *Manager) Destroy(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(CookieName); err == nil {
		m.mu.Lock()
		delete(m.sessions, c.Value)
		m.mu.Unlock()
	}
	http.SetCookie(w, &http.Cookie{Name: CookieName, Value: "", Path: "/", MaxAge: -1})
}

// DestroyAll logs out all sessions (e.g. after a password change).
func (m *Manager) DestroyAll() {
	m.mu.Lock()
	m.sessions = map[string]time.Time{}
	m.mu.Unlock()
}

func (m *Manager) Valid(r *http.Request) bool {
	c, err := r.Cookie(CookieName)
	if err != nil {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	exp, ok := m.sessions[c.Value]
	if !ok {
		return false
	}
	if time.Now().After(exp) {
		delete(m.sessions, c.Value)
		return false
	}
	return true
}

// SameOrigin protects state-changing requests against CSRF: if an Origin header
// is set, it must match the host.
func SameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	o := strings.TrimPrefix(strings.TrimPrefix(origin, "http://"), "https://")
	return o == r.Host
}
