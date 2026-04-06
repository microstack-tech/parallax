// Copyright 2026 The Parallax Authors
// This file is part of the Parallax library.

package backend

// GUIVersion is the user-facing version of the desktop app.
const GUIVersion = "0.1.0"

// GUIConfig is the persisted configuration that the wizard / settings screens
// edit. It is intentionally a flat struct so that Wails can serialise it to
// JSON without surprises.
type GUIConfig struct {
	Bootstrapped   bool   `json:"bootstrapped"`
	DataDir        string `json:"dataDir"`
	SyncMode       string `json:"syncMode"` // "snap" | "full"
	HTTPRPCEnabled bool   `json:"httpRpcEnabled"`
	HTTPRPCPort    int    `json:"httpRpcPort"`
	// BlockInbound, when true, disables NAT traversal so peers cannot dial
	// us. Stored as the negative ("block") so that the JSON zero value
	// (false = allow inbound) is the safe network-friendly default for
	// configs persisted before this field existed.
	BlockInbound bool `json:"blockInbound"`
	MaxPeers     int  `json:"maxPeers"`
	Theme          string `json:"theme"` // "system" | "light" | "dark"
	AutoStartNode  bool   `json:"autoStartNode"`

	// Advanced (only meaningful when AdvancedUnlocked is true in the UI).
	DatabaseCacheMB  int `json:"databaseCacheMB"`
	TrieCleanCacheMB int `json:"trieCleanCacheMB"`
	TrieDirtyCacheMB int `json:"trieDirtyCacheMB"`
	SnapshotCacheMB  int `json:"snapshotCacheMB"`
}

// NodeStatus is the high-level snapshot the dashboard polls.
type NodeStatus struct {
	Running       bool   `json:"running"`
	Syncing       bool   `json:"syncing"`
	CurrentBlock  uint64 `json:"currentBlock"`
	HighestBlock  uint64 `json:"highestBlock"`
	StartingBlock uint64 `json:"startingBlock"`
	Peers         int    `json:"peers"`
	ChainID       uint64 `json:"chainId"`
	NetworkID     uint64 `json:"networkId"`
	DataDir       string `json:"dataDir"`
	UptimeSeconds int64  `json:"uptimeSeconds"`
	DiskUsedBytes uint64 `json:"diskUsedBytes"`
	MemUsedBytes  uint64 `json:"memUsedBytes"`
	ClientVersion string `json:"clientVersion"`
	RPCEndpoint   string `json:"rpcEndpoint"` // empty if HTTP-RPC disabled
}

// NodeEvent is broadcast over the "node" Wails event channel.
type NodeEvent struct {
	Kind    string      `json:"kind"`
	Payload interface{} `json:"payload,omitempty"`
}

// LogLine is one tailed log entry streamed to the frontend.
type LogLine struct {
	Timestamp int64  `json:"ts"`
	Level     string `json:"level"`
	Message   string `json:"msg"`
}

// PeerView is one row of the dashboard's peer list.
//
// The dashboard's compact view uses ID, Name, RemoteAddr, Inbound, Caps.
// The full Peers screen additionally consumes FullID, Enode, LocalAddr,
// Trusted, Static, and Protocols (per-subprotocol metadata such as the
// peer's head hash and total difficulty for the prl protocol).
type PeerView struct {
	ID         string                 `json:"id"`         // 8-char short id
	FullID     string                 `json:"fullId"`     // full hex node id
	Enode      string                 `json:"enode"`      // full enode:// URL
	Name       string                 `json:"name"`
	RemoteAddr string                 `json:"remoteAddr"`
	LocalAddr  string                 `json:"localAddr"`
	Inbound    bool                   `json:"inbound"`
	Trusted    bool                   `json:"trusted"`
	Static     bool                   `json:"static"`
	Caps       []string               `json:"caps"`
	Protocols  map[string]interface{} `json:"protocols,omitempty"`
}

// TxView is one row of the dashboard's recent-transactions table.
type TxView struct {
	Hash      string `json:"hash"`
	Block     uint64 `json:"block"`
	Timestamp int64  `json:"timestamp"`
	From      string `json:"from"`
	To        string `json:"to"`        // empty for contract creation
	ValueWei  string `json:"valueWei"`
	GasUsed   uint64 `json:"gasUsed"`   // gas the tx requested (limit)
	Kind      string `json:"kind"`      // "transfer" | "contract" | "call"
}

// BlockView is one row of the dashboard's recent-blocks table.
type BlockView struct {
	Number      uint64 `json:"number"`
	Hash        string `json:"hash"`
	Timestamp   int64  `json:"timestamp"`
	TxCount     int    `json:"txCount"`
	GasUsed     uint64 `json:"gasUsed"`
	GasLimit    uint64 `json:"gasLimit"`
	SizeBytes   uint64 `json:"sizeBytes"`
	Coinbase    string `json:"coinbase"`
	RewardWei   string `json:"rewardWei"`   // base subsidy for this height
	Difficulty  string `json:"difficulty"`
}
