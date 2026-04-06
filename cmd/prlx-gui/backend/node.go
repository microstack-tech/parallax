// Copyright 2026 The Parallax Authors
// This file is part of the Parallax library.

package backend

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"sync"
	"time"

	"github.com/ParallaxProtocol/parallax/accounts/keystore"
	"github.com/ParallaxProtocol/parallax/consensus/xhash"
	"github.com/ParallaxProtocol/parallax/core/types"
	"github.com/ParallaxProtocol/parallax/log"
	"github.com/ParallaxProtocol/parallax/node"
	"github.com/ParallaxProtocol/parallax/p2p/enode"
	"github.com/ParallaxProtocol/parallax/p2p/nat"
	"github.com/ParallaxProtocol/parallax/params"
	"github.com/ParallaxProtocol/parallax/prl"
	"github.com/ParallaxProtocol/parallax/prl/downloader"
	"github.com/ParallaxProtocol/parallax/prl/prlconfig"
)

// NodeController owns the embedded node.Node + prl.Parallax instance and
// drives its lifecycle from the GUI. It is safe for concurrent use.
type NodeController struct {
	cfg *ConfigStore

	mu        sync.RWMutex
	stack     *node.Node
	parallax  *prl.Parallax
	startedAt time.Time

	emitter func(NodeEvent)
}

// NewNodeController creates a controller. Nothing is started until Start is
// called.
func NewNodeController(cfg *ConfigStore) *NodeController {
	return &NodeController{cfg: cfg}
}

// SetEmitter installs an event sink. Pass nil to disable.
func (n *NodeController) SetEmitter(fn func(NodeEvent)) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.emitter = fn
}

func (n *NodeController) emit(kind string, payload interface{}) {
	n.mu.RLock()
	fn := n.emitter
	n.mu.RUnlock()
	if fn != nil {
		fn(NodeEvent{Kind: kind, Payload: payload})
	}
}

// Parallax returns the running prl backend, or nil if the node is stopped.
// Other backend services use this to read chain state directly.
func (n *NodeController) Parallax() *prl.Parallax {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.parallax
}

// Stack returns the underlying node.Node, or nil if stopped.
func (n *NodeController) Stack() *node.Node {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.stack
}

// Start brings up the in-process node. It is idempotent: calling Start while
// already running returns nil.
func (n *NodeController) Start(ctx context.Context) error {
	n.mu.Lock()
	if n.stack != nil {
		n.mu.Unlock()
		return nil
	}
	guiCfg := n.cfg.Get()
	n.mu.Unlock()

	nodeCfg, prlCfg, err := buildConfigs(guiCfg)
	if err != nil {
		return err
	}

	stack, err := node.New(&nodeCfg)
	if err != nil {
		return fmt.Errorf("create node: %w", err)
	}
	if err := setAccountManagerBackends(stack); err != nil {
		stack.Close()
		return err
	}

	parallax, err := prl.New(stack, &prlCfg)
	if err != nil {
		stack.Close()
		return fmt.Errorf("register parallax service: %w", err)
	}

	if err := stack.Start(); err != nil {
		stack.Close()
		return fmt.Errorf("start node: %w", err)
	}

	n.mu.Lock()
	n.stack = stack
	n.parallax = parallax
	n.startedAt = time.Now()
	n.mu.Unlock()

	n.emit("started", nil)
	return nil
}

// Stop tears down the node. It is idempotent.
func (n *NodeController) Stop() error {
	n.mu.Lock()
	stack := n.stack
	n.stack = nil
	n.parallax = nil
	n.mu.Unlock()
	if stack == nil {
		return nil
	}
	err := stack.Close()
	n.emit("stopped", nil)
	return err
}

// ClientVersion returns the underlying parallax client version string —
// the same value `prlx version` would print, e.g. "1.1.2-unstable".
func (n *NodeController) ClientVersion() string {
	return params.VersionWithMeta
}

// Status returns the dashboard snapshot.
func (n *NodeController) Status() NodeStatus {
	n.mu.RLock()
	stack := n.stack
	parallax := n.parallax
	startedAt := n.startedAt
	n.mu.RUnlock()

	guiCfg := n.cfg.Get()
	st := NodeStatus{
		DataDir:       guiCfg.DataDir,
		ChainID:       params.MainnetChainConfig.ChainID.Uint64(),
		NetworkID:     2110,
		ClientVersion: "Parallax/" + GUIVersion,
	}
	if guiCfg.HTTPRPCEnabled {
		st.RPCEndpoint = fmt.Sprintf("http://127.0.0.1:%d", guiCfg.HTTPRPCPort)
	}
	if stack == nil || parallax == nil {
		return st
	}
	st.Running = true
	st.UptimeSeconds = int64(time.Since(startedAt).Seconds())
	st.Peers = stack.Server().PeerCount()

	// Sync status logic:
	//
	//   - Use the downloader's progress as the truth source. Progress.HighestBlock
	//     is set from peer head announcements during synchronisation, so
	//     CurrentBlock >= HighestBlock means our local chain has caught up
	//     to the highest tip any connected peer has reported. This is the
	//     same comparison eth_syncing uses.
	//   - Do NOT consult parallax.Synced() — that flag is gated on the first
	//     post-sync block import, so it stays false (and the dashboard would
	//     stay stuck on "Syncing") until the next block arrives, even when
	//     we are demonstrably at the tip.
	//   - Without peers we have no reference point, so claim "syncing" rather
	//     than falsely reporting "synced".
	prog := parallax.Downloader().Progress()
	st.CurrentBlock = prog.CurrentBlock
	st.HighestBlock = prog.HighestBlock
	st.StartingBlock = prog.StartingBlock

	if head := parallax.BlockChain().CurrentBlock(); head != nil {
		if head.NumberU64() > st.CurrentBlock {
			st.CurrentBlock = head.NumberU64()
		}
		if st.HighestBlock < st.CurrentBlock {
			st.HighestBlock = st.CurrentBlock
		}
	}

	caughtUp := st.HighestBlock == 0 || st.CurrentBlock >= st.HighestBlock
	st.Syncing = st.Peers == 0 || !caughtUp

	st.DiskUsedBytes = directorySize(guiCfg.DataDir)

	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	st.MemUsedBytes = ms.Alloc
	return st
}

// buildConfigs translates GUIConfig into a node.Config + prlconfig.Config pair.
func buildConfigs(g GUIConfig) (node.Config, prlconfig.Config, error) {
	nodeCfg := node.DefaultConfig
	nodeCfg.Name = "Parallax"
	nodeCfg.Version = GUIVersion
	nodeCfg.DataDir = g.DataDir
	nodeCfg.IPCPath = "prlx.ipc"
	nodeCfg.HTTPVirtualHosts = []string{"localhost"}
	nodeCfg.HTTPModules = []string{"net", "web3", "eth"}
	nodeCfg.WSModules = []string{"net", "web3", "eth"}
	if g.HTTPRPCEnabled {
		nodeCfg.HTTPHost = "127.0.0.1"
		nodeCfg.HTTPPort = g.HTTPRPCPort
	}
	if g.MaxPeers > 0 {
		nodeCfg.P2P.MaxPeers = g.MaxPeers
	}
	// nat.Any() opens an upnp/pmp port mapping so peers on the wider
	// internet can dial us. A nil NAT interface (the equivalent of
	// `--nat none` on the CLI) leaves us outbound-only — we still dial
	// peers but they cannot dial us.
	if g.BlockInbound {
		nodeCfg.P2P.NAT = nil
	} else {
		nodeCfg.P2P.NAT = nat.Any()
	}

	// Without bootstrap nodes the embedded node has nothing to dial; the
	// CLI binary wires these up via cmd/utils/flags.go. We replicate that
	// here so the GUI can actually find peers on a fresh install.
	nodeCfg.P2P.BootstrapNodes = parseEnodes(params.MainnetBootnodes)
	nodeCfg.P2P.BootstrapNodesV5 = parseEnodes(params.V5Bootnodes)

	prlCfg := prlconfig.Defaults
	switch g.SyncMode {
	case "full":
		prlCfg.SyncMode = downloader.FullSync
	case "light":
		prlCfg.SyncMode = downloader.LightSync
	default:
		prlCfg.SyncMode = downloader.SnapSync
	}
	if g.DatabaseCacheMB > 0 {
		prlCfg.DatabaseCache = g.DatabaseCacheMB
	}
	if g.TrieCleanCacheMB > 0 {
		prlCfg.TrieCleanCache = g.TrieCleanCacheMB
	}
	if g.TrieDirtyCacheMB > 0 {
		prlCfg.TrieDirtyCache = g.TrieDirtyCacheMB
	}
	if g.SnapshotCacheMB > 0 {
		prlCfg.SnapshotCache = g.SnapshotCacheMB
	}

	// DNS discovery: same default the CLI applies for mainnet. Without
	// these URLs the prl handler does not bootstrap a discovery iterator,
	// so peers are only ever found via the static bootstrap nodes above.
	if url := params.KnownDNSNetwork(params.MainnetGenesisHash, "all"); url != "" {
		prlCfg.ParallaxDiscoveryURLs = []string{url}
		prlCfg.SnapDiscoveryURLs = []string{url}
	}

	if prlCfg.SyncMode == downloader.LightSync {
		return nodeCfg, prlCfg, errors.New("light sync from GUI not supported yet")
	}
	return nodeCfg, prlCfg, nil
}

// parseEnodes converts a slice of enode URLs into resolved nodes, skipping
// (and logging) any malformed entries instead of crashing the GUI.
func parseEnodes(urls []string) []*enode.Node {
	out := make([]*enode.Node, 0, len(urls))
	for _, url := range urls {
		if url == "" {
			continue
		}
		n, err := enode.Parse(enode.ValidSchemes, url)
		if err != nil {
			log.Error("invalid bootstrap enode", "enode", url, "err", err)
			continue
		}
		out = append(out, n)
	}
	return out
}

// setAccountManagerBackends installs an empty-but-functional keystore
// backend so that the node.Node accounts manager has at least one backend
// registered. The MVP GUI does not surface any wallet functionality, but
// the underlying account manager still expects a backend to be present.
func setAccountManagerBackends(stack *node.Node) error {
	conf := stack.Config()
	keydir := stack.KeyStoreDir()
	scryptN := keystore.StandardScryptN
	scryptP := keystore.StandardScryptP
	if conf.UseLightweightKDF {
		scryptN = keystore.LightScryptN
		scryptP = keystore.LightScryptP
	}
	stack.AccountManager().AddBackend(keystore.NewKeyStore(keydir, scryptN, scryptP))
	return nil
}

// Peers returns a snapshot of the current peer set, suitable for both the
// dashboard's compact list and the full Peers screen. Rows are sorted by
// full node ID so that the order is stable across polls — without this,
// the underlying p2p.Server map iteration shuffles the list every refresh
// and the UI flickers.
func (n *NodeController) Peers() []PeerView {
	stack := n.Stack()
	if stack == nil {
		return []PeerView{}
	}
	srv := stack.Server()
	if srv == nil {
		return []PeerView{}
	}
	peers := srv.Peers()
	out := make([]PeerView, 0, len(peers))
	for _, p := range peers {
		info := p.Info()
		out = append(out, PeerView{
			ID:         info.ID[:8],
			FullID:     info.ID,
			Enode:      info.Enode,
			Name:       info.Name,
			RemoteAddr: info.Network.RemoteAddress,
			LocalAddr:  info.Network.LocalAddress,
			Inbound:    info.Network.Inbound,
			Trusted:    info.Network.Trusted,
			Static:     info.Network.Static,
			Caps:       info.Caps,
			Protocols:  info.Protocols,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].FullID < out[j].FullID })
	return out
}

// RecentBlocks returns up to n most recent canonical blocks (newest first).
// Walks the chain head backwards via the in-process BlockChain — no RPC.
func (n *NodeController) RecentBlocks(count int) []BlockView {
	if count <= 0 {
		count = 10
	}
	if count > 50 {
		count = 50
	}
	parallax := n.Parallax()
	if parallax == nil {
		return []BlockView{}
	}
	bc := parallax.BlockChain()
	head := bc.CurrentBlock()
	if head == nil {
		return []BlockView{}
	}
	out := make([]BlockView, 0, count)
	headNum := head.NumberU64()
	for i := 0; i < count; i++ {
		if uint64(i) > headNum {
			break
		}
		num := headNum - uint64(i)
		block := bc.GetBlockByNumber(num)
		if block == nil {
			continue
		}
		out = append(out, BlockView{
			Number:     num,
			Hash:       block.Hash().Hex(),
			Timestamp:  int64(block.Time()),
			TxCount:    len(block.Transactions()),
			GasUsed:    block.GasUsed(),
			GasLimit:   block.GasLimit(),
			SizeBytes:  uint64(block.Size()),
			Coinbase:   block.Coinbase().Hex(),
			RewardWei:  blockRewardAt(num).String(),
			Difficulty: block.Difficulty().String(),
		})
	}
	return out
}

// RecentTransactions walks the chain backwards from the head and collects up
// to `count` of the most recent transactions, newest first. We re-use the
// in-process BlockChain accessor instead of going through eth_getBlockByNumber
// so the dashboard refresh stays cheap.
func (n *NodeController) RecentTransactions(count int) []TxView {
	if count <= 0 {
		count = 6
	}
	if count > 100 {
		count = 100
	}
	parallax := n.Parallax()
	if parallax == nil {
		return []TxView{}
	}
	bc := parallax.BlockChain()
	head := bc.CurrentBlock()
	if head == nil {
		return []TxView{}
	}
	signer := types.LatestSignerForChainID(bc.Config().ChainID)

	out := make([]TxView, 0, count)
	headNum := head.NumberU64()

	// Bound the walk so we don't scan the entire history if recent blocks
	// are empty. ~200 blocks of look-back is plenty for a UI sample.
	for i := uint64(0); i < 200 && len(out) < count; i++ {
		if i > headNum {
			break
		}
		num := headNum - i
		block := bc.GetBlockByNumber(num)
		if block == nil {
			continue
		}
		ts := int64(block.Time())
		for _, tx := range block.Transactions() {
			if len(out) >= count {
				break
			}
			from, err := types.Sender(signer, tx)
			fromHex := ""
			if err == nil {
				fromHex = from.Hex()
			}
			to := ""
			kind := "call"
			if tx.To() == nil {
				kind = "contract"
			} else {
				to = tx.To().Hex()
				if len(tx.Data()) == 0 {
					kind = "transfer"
				}
			}
			out = append(out, TxView{
				Hash:      tx.Hash().Hex(),
				Block:     num,
				Timestamp: ts,
				From:      fromHex,
				To:        to,
				ValueWei:  tx.Value().String(),
				GasUsed:   tx.Gas(),
				Kind:      kind,
			})
		}
	}
	return out
}

// blockRewardAt mirrors consensus/xhash.calcBlockReward (which is unexported).
// We rely on the public constants InitialBlockRewardWei + HalvingIntervalBlocks
// so the two stay in lock-step.
func blockRewardAt(blockNumber uint64) *big.Int {
	if blockNumber == 0 {
		return new(big.Int)
	}
	reward := new(big.Int).Set(xhash.InitialBlockRewardWei)
	halvings := blockNumber / xhash.HalvingIntervalBlocks
	if halvings > 63 {
		return new(big.Int)
	}
	reward.Rsh(reward, uint(halvings))
	return reward
}

// directorySize sums the size of all files under root. Errors are swallowed
// because this only feeds a UI gauge.
func directorySize(root string) uint64 {
	if root == "" {
		return 0
	}
	var total uint64
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err == nil {
			total += uint64(info.Size())
		}
		return nil
	})
	return total
}
