// Package config manages the persistent panel configuration (config.json).
package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
)

type Settings struct {
	PasswordHash string `json:"passwordHash"`

	// Server
	JarName     string `json:"jarName"`
	ServerType  string `json:"serverType"` // vanilla | paper | custom
	MCVersion   string `json:"mcVersion"`
	JavaMode    string `json:"javaMode"`    // "auto" or path to a java binary
	JavaVersion int    `json:"javaVersion"` // required major version (auto mode)
	MinRAM      string `json:"minRam"`
	MaxRAM      string `json:"maxRam"`
	JVMArgs     string `json:"jvmArgs"`
	ServerArgs  string `json:"serverArgs"`
	Autostart   bool   `json:"autostart"`
	AutoRestart bool   `json:"autoRestart"`
}

type Store struct {
	mu      sync.RWMutex
	path    string
	DataDir string
	s       Settings
}

func defaults() Settings {
	return Settings{
		JarName:     "server.jar",
		JavaMode:    "auto",
		JavaVersion: 21,
		MinRAM:      "1G",
		MaxRAM:      "2G",
		ServerArgs:  "nogui",
	}
}

func Load(dataDir string) (*Store, error) {
	st := &Store{path: filepath.Join(dataDir, "config.json"), DataDir: dataDir, s: defaults()}
	b, err := os.ReadFile(st.path)
	if errors.Is(err, os.ErrNotExist) {
		return st, st.save()
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, &st.s); err != nil {
		return nil, err
	}
	return st, nil
}

func (st *Store) Get() Settings {
	st.mu.RLock()
	defer st.mu.RUnlock()
	return st.s
}

// Update changes the settings atomically and saves them.
func (st *Store) Update(fn func(*Settings)) error {
	st.mu.Lock()
	defer st.mu.Unlock()
	fn(&st.s)
	return st.save()
}

func (st *Store) save() error {
	b, err := json.MarshalIndent(st.s, "", "  ")
	if err != nil {
		return err
	}
	tmp := st.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, st.path)
}

func (st *Store) ServerDir() string { return filepath.Join(st.DataDir, "server") }
func (st *Store) JavaDir() string   { return filepath.Join(st.DataDir, "java") }
