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
	// Cache split mirrors what the CLI uses on mainnet: a 4096 MB total
	// budget partitioned by the same percentages as cmd/utils/flags.go
	// (50% db, 15% trie clean, 25% trie dirty, 10% snapshot). The CLI bumps
	// from its 1024 MB default to 4096 MB whenever syncmode != light AND no
	// explicit --cache flag is provided AND we're on mainnet — see
	// cmd/prlx/main.go:prepare(). Without this bump the GUI would sync
	// roughly four times slower than the CLI.
	const cacheTotalMB = 4096
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
		EnableSmartFee:   false,                   // opt-in via Settings → Fee estimation.
		DatabaseCacheMB:  cacheTotalMB * 50 / 100, // 2048 MB
		TrieCleanCacheMB: cacheTotalMB * 15 / 100, //  614 MB
		TrieDirtyCacheMB: cacheTotalMB * 25 / 100, // 1024 MB
		SnapshotCacheMB:  cacheTotalMB * 10 / 100, //  409 MB
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

	// One-time upgrade: an earlier release of the GUI shipped a 1024 MB
	// cache split (512 / 154 / 256 / 102). The CLI uses 4096 MB on mainnet,
	// which sync benchmarks show is roughly 4× faster. If we recognise the
	// stale tuple exactly, replace it with the new defaults so existing
	// installs benefit on the next launch. Users who have explicitly
	// customised any of these values keep their overrides.
	if cfg.DatabaseCacheMB == 512 &&
		cfg.TrieCleanCacheMB == 154 &&
		cfg.TrieDirtyCacheMB == 256 &&
		cfg.SnapshotCacheMB == 102 {
		cfg.DatabaseCacheMB = d.DatabaseCacheMB
		cfg.TrieCleanCacheMB = d.TrieCleanCacheMB
		cfg.TrieDirtyCacheMB = d.TrieDirtyCacheMB
		cfg.SnapshotCacheMB = d.SnapshotCacheMB
	}
}
