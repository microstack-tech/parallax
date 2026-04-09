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
	BlockInbound  bool   `json:"blockInbound"`
	MaxPeers      int    `json:"maxPeers"`
	Theme         string `json:"theme"` // "system" | "light" | "dark"
	AutoStartNode bool   `json:"autoStartNode"`

	// EnableSmartFee turns on the Bitcoin Core-style smart fee estimator
	// in the gas-price oracle (gasprice.Config.EnableSmartFeeEstimator).
	// Off by default — same default as the CLI's --gpo.smartfee flag.
	// Requires a node restart to take effect because the oracle is built
	// once at prl.New() time.
	EnableSmartFee bool `json:"enableSmartFee"`

	// Advanced (only meaningful when AdvancedUnlocked is true in the UI).
	DatabaseCacheMB  int `json:"databaseCacheMB"`
	TrieCleanCacheMB int `json:"trieCleanCacheMB"`
	TrieDirtyCacheMB int `json:"trieDirtyCacheMB"`
	SnapshotCacheMB  int `json:"snapshotCacheMB"`

	// Mining
	MiningWallet  string     `json:"miningWallet"`  // 0x... reward address
	MiningWorker  string     `json:"miningWorker"`  // worker name (defaults to hostname)
	MiningPool    string     `json:"miningPool"`    // selected pool URL
	MiningThreads int        `json:"miningThreads"` // CPU threads for solo mode (0 = auto)
	MiningDevices []int      `json:"miningDevices"` // GPU device indices (nil = all)
	CustomPools   []PoolInfo `json:"customPools"`   // user-added pool entries
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
	ID         string                 `json:"id"`     // 8-char short id
	FullID     string                 `json:"fullId"` // full hex node id
	Enode      string                 `json:"enode"`  // full enode:// URL
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
	To        string `json:"to"` // empty for contract creation
	ValueWei  string `json:"valueWei"`
	GasUsed   uint64 `json:"gasUsed"` // gas the tx requested (limit)
	Kind      string `json:"kind"`    // "transfer" | "contract" | "call"
}

// BlockView is one row of the dashboard's recent-blocks table.
type BlockView struct {
	Number     uint64 `json:"number"`
	Hash       string `json:"hash"`
	Timestamp  int64  `json:"timestamp"`
	TxCount    int    `json:"txCount"`
	GasUsed    uint64 `json:"gasUsed"`
	GasLimit   uint64 `json:"gasLimit"`
	SizeBytes  uint64 `json:"sizeBytes"`
	Coinbase   string `json:"coinbase"`
	RewardWei  string `json:"rewardWei"` // base subsidy for this height
	Difficulty string `json:"difficulty"`
}

// ---------------------------------------------------------------------------
// Mining
// ---------------------------------------------------------------------------

// PoolInfo describes one mining pool endpoint.
type PoolInfo struct {
	Name    string `json:"name"`
	URL     string `json:"url"`     // stratum+tcp://host:port
	Region  string `json:"region"`  // "US", "EU", "Asia", etc.
	Builtin bool   `json:"builtin"` // true = shipped with app
}

// DeviceInfo describes one GPU detected by hashwarp --list-devices.
type DeviceInfo struct {
	Index  int    `json:"index"`
	PciID  string `json:"pciId"`  // "01:00.0"
	Type   string `json:"type"`   // "Gpu" | "Cpu" | "Acc"
	Name   string `json:"name"`
	Memory string `json:"memory"` // "8.00 GB"
	CUDA   bool   `json:"cuda"`
	OpenCL bool   `json:"openCl"`
}

// GPUDeviceStatus is per-device live stats from hashwarp API.
type GPUDeviceStatus struct {
	Index    int     `json:"index"`
	Name     string  `json:"name"`
	Mode     string  `json:"mode"` // "CUDA" | "OpenCL"
	Hashrate float64 `json:"hashrate"`
	TempC    int     `json:"tempC"`
	FanPct   int     `json:"fanPct"`
	PowerW   float64 `json:"powerW"`
	Paused   bool    `json:"paused"`
	Accepted uint64  `json:"accepted"`
	Rejected uint64  `json:"rejected"`
	Failed   uint64  `json:"failed"`
}

// MinerStatus is the unified snapshot polled by Mining.tsx every 2s.
type MinerStatus struct {
	Mode    string `json:"mode"`    // "off" | "pool" | "solo"
	Running bool   `json:"running"`

	// Aggregate
	Hashrate      float64 `json:"hashrate"` // H/s
	UptimeSeconds int64   `json:"uptimeSeconds"`

	// Pool mode (GPU via hashwarp)
	PoolConnected      bool              `json:"poolConnected"`
	PoolURI            string            `json:"poolUri"`
	SharesAccepted     uint64            `json:"sharesAccepted"`
	SharesRejected     uint64            `json:"sharesRejected"`
	SharesFailed       uint64            `json:"sharesFailed"`
	SecsSinceLastShare uint64            `json:"secsSinceLastShare"`
	Epoch              int               `json:"epoch"`
	GeneratingDAG      bool              `json:"generatingDag"`
	Devices            []GPUDeviceStatus `json:"devices"`

	// Solo mode (CPU via embedded node)
	SoloDifficulty  string `json:"soloDifficulty"`
	SoloBlocksFound uint64 `json:"soloBlocksFound"`

	// Error surfaced to UI
	Error string `json:"error,omitempty"`
}

// MinerEvent is broadcast over the "miner" Wails event channel.
type MinerEvent struct {
	Kind    string      `json:"kind"` // "started" | "stopped" | "error" | "dag" | "stats"
	Payload interface{} `json:"payload,omitempty"`
}
