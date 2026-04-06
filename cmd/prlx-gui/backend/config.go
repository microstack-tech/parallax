// Copyright 2026 The Parallax Authors
// This file is part of the Parallax library.

package backend

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"

	"github.com/ParallaxProtocol/parallax/node"
)

// ConfigStore persists GUIConfig to <userConfigDir>/Parallax/gui.json. The
// node datadir is treated as configuration too: it can be moved between runs
// and the GUI honours the user's choice instead of always using
// node.DefaultDataDir.
type ConfigStore struct {
	mu   sync.RWMutex
	path string
	cfg  GUIConfig
}

// LoadConfigStore opens (or creates) the config store. If path is empty, the
// platform's user config dir is used.
func LoadConfigStore(path string) (*ConfigStore, error) {
	if path == "" {
		dir, err := os.UserConfigDir()
		if err != nil {
			return nil, err
		}
		path = filepath.Join(dir, "Parallax", "gui.json")
	}
	cs := &ConfigStore{path: path, cfg: defaultGUIConfig()}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}

	data, err := os.ReadFile(path)
	switch {
	case os.IsNotExist(err):
		// Fresh install — leave defaults; Bootstrapped stays false so the
		// wizard will run.
		return cs, nil
	case err != nil:
		return nil, err
	}

	var loaded GUIConfig
	if err := json.Unmarshal(data, &loaded); err != nil {
		return nil, err
	}
	mergeDefaults(&loaded)
	cs.cfg = loaded
	return cs, nil
}

// Get returns a copy of the current config.
func (c *ConfigStore) Get() GUIConfig {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.cfg
}

// Save writes the config atomically and updates the in-memory copy.
func (c *ConfigStore) Save(cfg GUIConfig) error {
	mergeDefaults(&cfg)

	c.mu.Lock()
	defer c.mu.Unlock()

	tmp := c.path + ".tmp"
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, c.path); err != nil {
		return err
	}
	c.cfg = cfg
	return nil
}

func defaultGUIConfig() GUIConfig {
	return GUIConfig{
		Bootstrapped:     false,
		DataDir:          node.DefaultDataDir(),
		SyncMode:         "snap",
		HTTPRPCEnabled:   true, // MVP: on by default so MetaMask can connect.
		HTTPRPCPort:      8545,
		BlockInbound:     false, // help the network by accepting dials by default.
		MaxPeers:         100,
		Theme:            "system",
		AutoStartNode:    true,
		DatabaseCacheMB:  512,
		TrieCleanCacheMB: 154,
		TrieDirtyCacheMB: 256,
		SnapshotCacheMB:  102,
	}
}

func mergeDefaults(cfg *GUIConfig) {
	d := defaultGUIConfig()
	if cfg.DataDir == "" {
		cfg.DataDir = d.DataDir
	}
	if cfg.SyncMode == "" {
		cfg.SyncMode = d.SyncMode
	}
	if cfg.HTTPRPCPort == 0 {
		cfg.HTTPRPCPort = d.HTTPRPCPort
	}
	if cfg.MaxPeers == 0 {
		cfg.MaxPeers = d.MaxPeers
	}
	if cfg.Theme == "" {
		cfg.Theme = d.Theme
	}
	if cfg.DatabaseCacheMB == 0 {
		cfg.DatabaseCacheMB = d.DatabaseCacheMB
	}
	if cfg.TrieCleanCacheMB == 0 {
		cfg.TrieCleanCacheMB = d.TrieCleanCacheMB
	}
	if cfg.TrieDirtyCacheMB == 0 {
		cfg.TrieDirtyCacheMB = d.TrieDirtyCacheMB
	}
	if cfg.SnapshotCacheMB == 0 {
		cfg.SnapshotCacheMB = d.SnapshotCacheMB
	}
}
