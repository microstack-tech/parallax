// Copyright 2025-2026 The Parallax Protocol Authors
// This file is part of parallax.
//
// parallax is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// parallax is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU General Public License for more details.
//
// You should have received a copy of the GNU General Public License
// along with parallax. If not, see <http://www.gnu.org/licenses/>.

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ParallaxProtocol/parallax/cmd/utils"
	"github.com/ParallaxProtocol/parallax/p2p"
	"github.com/ParallaxProtocol/parallax/rpc"
	"gopkg.in/urfave/cli.v1"
)

// Output conventions match bitcoin-cli:
//
//   - Object/array responses are emitted as pretty-printed JSON (2-space
//     indent), so they compose cleanly with jq and are diff-friendly.
//   - Scalar responses (integers, hex strings, decimal amounts) are printed
//     bare, followed by a newline, so shell scripts can consume them with
//     $(parallax blockcount) without parsing JSON.
//   - Mutating commands (stop, addpeer, removepeer) print nothing on
//     success and exit 0. Errors go to stderr with exit 1.
//
// See bitcoin-cli's `getblockchaininfo` (object), `getblockcount` (scalar),
// `sendrawtransaction` (scalar hex), `addnode` (silent) for the originals.

// clientRPCFlag lets the user override the RPC endpoint used by the sugar
// subcommands below. When unset, the commands look for <datadir>/parallax.ipc —
// matching the behaviour of `parallax attach`.
var clientRPCFlag = cli.StringFlag{
	Name:  "rpc",
	Usage: "RPC endpoint to contact (default: <datadir>/parallax.ipc). Accepts file paths (IPC) or http://..., ws:// URLs.",
}

var clientCommandFlags = []cli.Flag{utils.DataDirFlag, utils.TestnetFlag, clientRPCFlag}

// weiFlag switches `balance` and similar amount-returning commands from the
// bitcoin-style decimal display (1.234… LAX) to raw wei integers, for
// callers that want exact values without floating-point concerns.
var weiFlag = cli.BoolFlag{
	Name:  "wei",
	Usage: "Print amounts in wei (integer) instead of LAX (decimal)",
}

// blockTagFlag selects which historical block account-state lookups should
// be evaluated against. Accepts the same values eth_* RPCs take: a decimal
// block number, a 0x-prefixed hex number, or latest/earliest/pending/safe/
// finalized. Defaults to "latest" to match bitcoin-cli's "current balance"
// default.
var blockTagFlag = cli.StringFlag{
	Name:  "block",
	Usage: "Block tag or number to query against (default: latest)",
	Value: "latest",
}

var (
	stopCommand = cli.Command{
		Action:    utils.MigrateFlags(clientStop),
		Name:      "stop",
		Usage:     "Gracefully stop a running parallax daemon",
		ArgsUsage: " ",
		Flags:     clientCommandFlags,
		Category:  "CLIENT COMMANDS",
		Description: `
Sends admin_stop over RPC to a running node, asking it to shut down
gracefully. Exits non-zero if the node cannot be reached.`,
	}

	infoCommand = cli.Command{
		Action:    utils.MigrateFlags(clientInfo),
		Name:      "info",
		Usage:     "Combined overview of chain, network and mempool state",
		ArgsUsage: " ",
		Flags:     clientCommandFlags,
		Category:  "CLIENT COMMANDS",
		Description: `
Equivalent to bitcoin-cli's getblockchaininfo + getnetworkinfo +
getmempoolinfo merged into one call.`,
	}

	chainInfoCommand = cli.Command{
		Action:    utils.MigrateFlags(clientChainInfo),
		Name:      "chaininfo",
		Usage:     "Chain state summary (height, tip, difficulty, total difficulty)",
		ArgsUsage: " ",
		Flags:     clientCommandFlags,
		Category:  "CLIENT COMMANDS",
		Description: `
Equivalent to bitcoin-cli's getblockchaininfo, scoped to chain state only
(no network or mempool fields).`,
	}

	netInfoCommand = cli.Command{
		Action:    utils.MigrateFlags(clientNetInfo),
		Name:      "netinfo",
		Usage:     "Network state summary (peer count, listen addr, enode, NAT)",
		ArgsUsage: " ",
		Flags:     clientCommandFlags,
		Category:  "CLIENT COMMANDS",
		Description: `
Equivalent to bitcoin-cli's getnetworkinfo.`,
	}

	uptimeCommand = cli.Command{
		Action:    utils.MigrateFlags(clientUptime),
		Name:      "uptime",
		Usage:     "Seconds since the running node started",
		ArgsUsage: " ",
		Flags:     clientCommandFlags,
		Category:  "CLIENT COMMANDS",
		Description: `
Equivalent to bitcoin-cli's uptime. Prints a bare integer.`,
	}

	peersCommand = cli.Command{
		Action:    utils.MigrateFlags(clientPeers),
		Name:      "peers",
		Usage:     "List peers connected to the running parallax node",
		ArgsUsage: " ",
		Flags:     clientCommandFlags,
		Category:  "CLIENT COMMANDS",
	}

	blockCountCommand = cli.Command{
		Action:    utils.MigrateFlags(clientBlockCount),
		Name:      "blockcount",
		Usage:     "Print the latest block number known to the running node",
		ArgsUsage: " ",
		Flags:     clientCommandFlags,
		Category:  "CLIENT COMMANDS",
	}

	syncingCommand = cli.Command{
		Action:    utils.MigrateFlags(clientSyncing),
		Name:      "syncing",
		Usage:     "Show sync progress of the running node",
		ArgsUsage: " ",
		Flags:     clientCommandFlags,
		Category:  "CLIENT COMMANDS",
	}

	mempoolCommand = cli.Command{
		Action:    utils.MigrateFlags(clientMempool),
		Name:      "mempool",
		Usage:     "Summary of the node's transaction pool",
		ArgsUsage: " ",
		Flags:     clientCommandFlags,
		Category:  "CLIENT COMMANDS",
	}

	mempoolContentCommand = cli.Command{
		Action:    utils.MigrateFlags(clientMempoolContent),
		Name:      "mempool-content",
		Usage:     "Dump the transaction pool contents (optionally filtered by sender)",
		ArgsUsage: " ",
		Flags: append([]cli.Flag{
			cli.StringFlag{Name: "address", Usage: "Filter by sender address (0x…); omit for the full pool"},
			cli.BoolFlag{Name: "inspect", Usage: "Return compact string summaries instead of full transaction objects"},
		}, clientCommandFlags...),
		Category: "CLIENT COMMANDS",
		Description: `
Equivalent to bitcoin-cli's getrawmempool with --verbose, for the EVM
model. Output is a JSON object {pending, queued} keyed by address and
nonce. The full dump can be large on a busy node; use --address to scope
it to a single sender or --inspect for one-line summaries.`,
	}

	estimateGasCommand = cli.Command{
		Action:    utils.MigrateFlags(clientEstimateGas),
		Name:      "estimategas",
		Usage:     "Estimate the gas required to execute a transaction",
		ArgsUsage: "<to>",
		Flags: append([]cli.Flag{
			cli.StringFlag{Name: "from", Usage: "Sender address (0x…); defaults to the zero address"},
			cli.StringFlag{Name: "value", Usage: "Value in wei (integer or 0x-prefixed hex). Defaults to 0."},
			cli.StringFlag{Name: "data", Usage: "Call data as hex (0x-prefixed). Defaults to empty."},
		}, clientCommandFlags...),
		Category: "CLIENT COMMANDS",
		Description: `
Calls eth_estimateGas and prints the estimated gas units as a bare
integer, matching the scalar-output convention used by `+"`gasprice`"+`.`,
	}

	balanceCommand = cli.Command{
		Action:    utils.MigrateFlags(clientBalance),
		Name:      "balance",
		Usage:     "Print the balance of an account",
		ArgsUsage: "<address>",
		Flags:     append([]cli.Flag{weiFlag, blockTagFlag}, clientCommandFlags...),
		Category:  "CLIENT COMMANDS",
	}

	nonceCommand = cli.Command{
		Action:    utils.MigrateFlags(clientNonce),
		Name:      "nonce",
		Usage:     "Print the transaction count (nonce) of an account",
		ArgsUsage: "<address>",
		Flags:     append([]cli.Flag{blockTagFlag}, clientCommandFlags...),
		Category:  "CLIENT COMMANDS",
		Description: `
Equivalent to eth_getTransactionCount. Use this to craft raw transactions
or debug stuck transactions.`,
	}

	codeCommand = cli.Command{
		Action:    utils.MigrateFlags(clientCode),
		Name:      "code",
		Usage:     "Print the deployed bytecode at an address",
		ArgsUsage: "<address>",
		Flags:     append([]cli.Flag{blockTagFlag}, clientCommandFlags...),
		Category:  "CLIENT COMMANDS",
		Description: `
Returns the contract bytecode as a 0x-prefixed hex string, or "0x" if
the address is an externally owned account (EOA). Uses eth_getCode.`,
	}

	storageCommand = cli.Command{
		Action:    utils.MigrateFlags(clientStorage),
		Name:      "storage",
		Usage:     "Read a 32-byte storage slot from a contract",
		ArgsUsage: "<address> <slot>",
		Flags:     append([]cli.Flag{blockTagFlag}, clientCommandFlags...),
		Category:  "CLIENT COMMANDS",
		Description: `
Returns the 32-byte value at the given storage slot as a 0x-prefixed
hex string. <slot> accepts a decimal index or a 0x-prefixed hex key.
Uses eth_getStorageAt.`,
	}

	getTxCommand = cli.Command{
		Action:    utils.MigrateFlags(clientGetTx),
		Name:      "gettx",
		Usage:     "Fetch a transaction by hash",
		ArgsUsage: "<txhash>",
		Flags:     clientCommandFlags,
		Category:  "CLIENT COMMANDS",
	}

	getReceiptCommand = cli.Command{
		Action:    utils.MigrateFlags(clientGetReceipt),
		Name:      "getreceipt",
		Usage:     "Fetch a transaction receipt by hash",
		ArgsUsage: "<txhash>",
		Flags:     clientCommandFlags,
		Category:  "CLIENT COMMANDS",
	}

	getBlockCommand = cli.Command{
		Action:    utils.MigrateFlags(clientGetBlock),
		Name:      "getblock",
		Usage:     "Fetch a block by number or hash",
		ArgsUsage: "<number|hash> [--full]",
		Flags: append([]cli.Flag{
			cli.BoolFlag{Name: "full", Usage: "Include full transaction objects instead of only hashes"},
		}, clientCommandFlags...),
		Category: "CLIENT COMMANDS",
	}

	getBlockHashCommand = cli.Command{
		Action:    utils.MigrateFlags(clientGetBlockHash),
		Name:      "getblockhash",
		Usage:     "Print the canonical hash of the block at a given height",
		ArgsUsage: "<number>",
		Flags:     clientCommandFlags,
		Category:  "CLIENT COMMANDS",
		Description: `
Equivalent to bitcoin-cli getblockhash. Prints a bare hex hash (0x…).`,
	}

	getHeaderCommand = cli.Command{
		Action:    utils.MigrateFlags(clientGetHeader),
		Name:      "getheader",
		Usage:     "Fetch a block header by number or hash",
		ArgsUsage: "<number|hash>",
		Flags:     clientCommandFlags,
		Category:  "CLIENT COMMANDS",
		Description: `
Equivalent to bitcoin-cli getblockheader. Returns the block header as
JSON without the transactions list — smaller and faster than getblock
when you only need metadata.`,
	}

	tipCommand = cli.Command{
		Action:    utils.MigrateFlags(clientTip),
		Name:      "tip",
		Usage:     "Summary of the latest canonical block",
		ArgsUsage: " ",
		Flags:     clientCommandFlags,
		Category:  "CLIENT COMMANDS",
		Description: `
Condensed JSON view of the chain tip: number, hash, parent, timestamp,
miner, gas used and limit, transaction count. Handy at the top of ops
scripts or in health probes.`,
	}

	gasPriceCommand = cli.Command{
		Action:    utils.MigrateFlags(clientGasPrice),
		Name:      "gasprice",
		Usage:     "Print the current suggested gas price (wei)",
		ArgsUsage: " ",
		Flags:     clientCommandFlags,
		Category:  "CLIENT COMMANDS",
	}

	sendRawCommand = cli.Command{
		Action:    utils.MigrateFlags(clientSendRaw),
		Name:      "sendraw",
		Usage:     "Submit a signed, hex-encoded transaction and print the hash",
		ArgsUsage: "<hex>",
		Flags:     clientCommandFlags,
		Category:  "CLIENT COMMANDS",
	}

	addPeerCommand = cli.Command{
		Action:    utils.MigrateFlags(clientAddPeer),
		Name:      "addpeer",
		Usage:     "Add a static peer by enode URL",
		ArgsUsage: "<enode>",
		Flags:     clientCommandFlags,
		Category:  "CLIENT COMMANDS",
	}

	removePeerCommand = cli.Command{
		Action:    utils.MigrateFlags(clientRemovePeer),
		Name:      "removepeer",
		Usage:     "Remove a static peer by enode URL",
		ArgsUsage: "<enode>",
		Flags:     clientCommandFlags,
		Category:  "CLIENT COMMANDS",
	}
)

// clientSugarCommands is the set of RPC-client subcommands registered on
// the app. Exported as a slice to keep the main.go command list compact.
var clientSugarCommands = []cli.Command{
	stopCommand,
	infoCommand,
	chainInfoCommand,
	netInfoCommand,
	uptimeCommand,
	peersCommand,
	blockCountCommand,
	syncingCommand,
	mempoolCommand,
	mempoolContentCommand,
	balanceCommand,
	nonceCommand,
	codeCommand,
	storageCommand,
	estimateGasCommand,
	getTxCommand,
	getReceiptCommand,
	getBlockCommand,
	getBlockHashCommand,
	getHeaderCommand,
	tipCommand,
	gasPriceCommand,
	sendRawCommand,
	addPeerCommand,
	removePeerCommand,
}

// ----------------------------------------------------------------------------
// Connection helpers
// ----------------------------------------------------------------------------

// clientEndpoint resolves the RPC endpoint for a client subcommand. It
// prefers an explicit --rpc flag, then falls back to <datadir>/parallax.ipc
// (honoring --testnet).
func clientEndpoint(ctx *cli.Context) string {
	if ep := ctx.String(clientRPCFlag.Name); ep != "" {
		return ep
	}
	datadir := ctx.GlobalString(utils.DataDirFlag.Name)
	if datadir == "" {
		datadir = ctx.String(utils.DataDirFlag.Name)
	}
	if datadir == "" {
		datadir = "."
	}
	if ctx.Bool(utils.TestnetFlag.Name) || ctx.GlobalBool(utils.TestnetFlag.Name) {
		datadir = filepath.Join(datadir, "testnet")
	}
	return filepath.Join(datadir, "parallax.ipc")
}

// dialClient opens an RPC connection to the endpoint chosen by
// clientEndpoint, with a short connect deadline so the CLI never hangs
// waiting for an unreachable daemon.
func dialClient(ctx *cli.Context) (*rpc.Client, string, error) {
	endpoint := clientEndpoint(ctx)
	dctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client, err := rpc.DialContext(dctx, endpoint)
	if err != nil {
		hint := ""
		if !strings.Contains(endpoint, "://") {
			hint = "\n(is the node running? check with: ls -l " + endpoint + ")"
		}
		return nil, endpoint, fmt.Errorf("cannot connect to %s: %v%s", endpoint, err, hint)
	}
	return client, endpoint, nil
}

// callRPC dispatches a single RPC call against a freshly dialled client
// and closes it afterwards. Returns the raw result in the provided pointer.
func callRPC(ctx *cli.Context, out interface{}, method string, args ...interface{}) error {
	client, _, err := dialClient(ctx)
	if err != nil {
		return err
	}
	defer client.Close()
	cctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return client.CallContext(cctx, out, method, args...)
}

// ----------------------------------------------------------------------------
// Output helpers — bitcoin-cli style
// ----------------------------------------------------------------------------

// printJSON writes v as pretty-printed JSON to stdout, using the same
// formatting bitcoin-cli uses for object responses: 2-space indent, HTML
// escaping disabled (so addresses like 0x… aren't mangled).
func printJSON(v interface{}) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}

// printScalar writes a bare value + newline. Used for commands whose
// bitcoin-cli analogs return a single number, hash or address
// (getblockcount, getblockhash, sendrawtransaction, getbalance, …).
func printScalar(v interface{}) {
	fmt.Println(v)
}

// hexToBigInt parses a 0x-prefixed hex string into a *big.Int. Returns
// nil for empty input rather than erroring — status-display contexts
// prefer silent zeros over noisy failures.
func hexToBigInt(s string) *big.Int {
	s = strings.TrimPrefix(s, "0x")
	if s == "" {
		return new(big.Int)
	}
	n := new(big.Int)
	if _, ok := n.SetString(s, 16); !ok {
		return new(big.Int)
	}
	return n
}

// hexToUint parses a hex-prefixed uint, returning 0 on parse failure.
func hexToUint(s string) uint64 {
	return hexToBigInt(s).Uint64()
}

// weiToLAX formats a wei amount as a fixed-point LAX decimal string
// (18 decimals), trimming trailing zeros to match bitcoin-cli's amount
// display (which trims e.g. "0.12300000" → "0.123"). Always preserves at
// least one fractional digit ("1.0", not "1") to signal this is a
// currency value, matching bitcoin-cli's "0.00000000" for zero balance.
func weiToLAX(wei *big.Int) string {
	const decimals = 18
	if wei == nil {
		wei = new(big.Int)
	}
	neg := wei.Sign() < 0
	abs := new(big.Int).Abs(wei)

	divisor := new(big.Int).Exp(big.NewInt(10), big.NewInt(decimals), nil)
	intPart, fracPart := new(big.Int).QuoRem(abs, divisor, new(big.Int))

	frac := fmt.Sprintf("%0*s", decimals, fracPart.String())
	frac = strings.TrimRight(frac, "0")
	if frac == "" {
		frac = "0"
	}
	sign := ""
	if neg {
		sign = "-"
	}
	return sign + intPart.String() + "." + frac
}

// isConnectionClosed heuristically detects "server went away" errors that
// are expected after admin_stop, since the RPC server tears down before
// the reply can always round-trip.
func isConnectionClosed(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "EOF") ||
		strings.Contains(msg, "connection reset") ||
		strings.Contains(msg, "broken pipe") ||
		strings.Contains(msg, "use of closed")
}

// requireArg returns the first positional argument or an error if missing.
func requireArg(ctx *cli.Context, name string) (string, error) {
	arg := ctx.Args().First()
	if arg == "" {
		return "", fmt.Errorf("missing required argument: %s", name)
	}
	return arg, nil
}

// ----------------------------------------------------------------------------
// Commands
// ----------------------------------------------------------------------------

func clientStop(ctx *cli.Context) error {
	client, endpoint, err := dialClient(ctx)
	if err != nil {
		return err
	}
	defer client.Close()

	// admin_stop triggers shutdown on a short delay and returns true. The
	// RPC call itself may also error out with a connection reset if the
	// server closes the socket before the response round-trips; treat that
	// as success since shutdown was in fact initiated.
	cctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var ok bool
	err = client.CallContext(cctx, &ok, "admin_stop")
	if err != nil && !isConnectionClosed(err) {
		return fmt.Errorf("admin_stop on %s: %v", endpoint, err)
	}
	// bitcoin-cli prints "Bitcoin Core stopping" — mirror that convention.
	fmt.Println("Parallax server stopping")
	return nil
}

func clientInfo(ctx *cli.Context) error {
	client, _, err := dialClient(ctx)
	if err != nil {
		return err
	}
	defer client.Close()
	cctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var (
		chainIDHex string
		blockHex   string
		peerHex    string
		gasHex     string
		netVersion string
		mining     bool
		syncing    json.RawMessage
		mempool    map[string]string
		nodeInfo   p2p.NodeInfo
	)
	// Collect each field independently so a single missing RPC doesn't
	// blank the whole report.
	_ = client.CallContext(cctx, &chainIDHex, "eth_chainId")
	_ = client.CallContext(cctx, &blockHex, "eth_blockNumber")
	_ = client.CallContext(cctx, &peerHex, "net_peerCount")
	_ = client.CallContext(cctx, &gasHex, "eth_gasPrice")
	_ = client.CallContext(cctx, &netVersion, "net_version")
	_ = client.CallContext(cctx, &mining, "eth_mining")
	_ = client.CallContext(cctx, &syncing, "eth_syncing")
	_ = client.CallContext(cctx, &mempool, "txpool_status")
	_ = client.CallContext(cctx, &nodeInfo, "admin_nodeInfo")

	var syncVal interface{} = false
	if len(syncing) > 0 && string(syncing) != "false" {
		_ = json.Unmarshal(syncing, &syncVal)
	}

	out := map[string]interface{}{
		"version":         nodeInfo.Name,
		"protocolversion": 66,
		"chainid":         hexToUint(chainIDHex),
		"networkid":       netVersion,
		"blocks":          hexToUint(blockHex),
		"connections":     hexToUint(peerHex),
		"gasprice":        hexToBigInt(gasHex).String(),
		"mining":          mining,
		"syncing":         syncVal,
		"enode":           nodeInfo.Enode,
		"id":              nodeInfo.ID,
		"listenaddr":      nodeInfo.ListenAddr,
		"mempool": map[string]uint64{
			"pending": hexToUint(mempool["pending"]),
			"queued":  hexToUint(mempool["queued"]),
		},
	}
	return printJSON(out)
}

func clientChainInfo(ctx *cli.Context) error {
	client, _, err := dialClient(ctx)
	if err != nil {
		return err
	}
	defer client.Close()
	cctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var (
		chainIDHex string
		netVersion string
		blockHex   string
		syncing    json.RawMessage
		head       map[string]interface{}
	)
	_ = client.CallContext(cctx, &chainIDHex, "eth_chainId")
	_ = client.CallContext(cctx, &netVersion, "net_version")
	_ = client.CallContext(cctx, &blockHex, "eth_blockNumber")
	_ = client.CallContext(cctx, &syncing, "eth_syncing")
	// Pull tip metadata (hash + difficulty + td) from the latest block
	// header. false = tx hashes only, which is all we need.
	_ = client.CallContext(cctx, &head, "eth_getBlockByNumber", "latest", false)

	var syncVal interface{} = false
	if len(syncing) > 0 && string(syncing) != "false" {
		_ = json.Unmarshal(syncing, &syncVal)
	}

	out := map[string]interface{}{
		"chainid":         hexToUint(chainIDHex),
		"networkid":       netVersion,
		"blocks":          hexToUint(blockHex),
		"bestblockhash":   stringField(head, "hash"),
		"difficulty":      stringField(head, "difficulty"),
		"totaldifficulty": stringField(head, "totalDifficulty"),
		"timestamp":       hexToUint(stringField(head, "timestamp")),
		"gasused":         hexToUint(stringField(head, "gasUsed")),
		"gaslimit":        hexToUint(stringField(head, "gasLimit")),
		"syncing":         syncVal,
	}
	return printJSON(out)
}

func clientNetInfo(ctx *cli.Context) error {
	client, _, err := dialClient(ctx)
	if err != nil {
		return err
	}
	defer client.Close()
	cctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var (
		peerHex  string
		listen   bool
		nodeInfo p2p.NodeInfo
	)
	_ = client.CallContext(cctx, &peerHex, "net_peerCount")
	_ = client.CallContext(cctx, &listen, "net_listening")
	_ = client.CallContext(cctx, &nodeInfo, "admin_nodeInfo")

	out := map[string]interface{}{
		"version":         nodeInfo.Name,
		"protocolversion": 66,
		"connections":     hexToUint(peerHex),
		"listening":       listen,
		"listenaddr":      nodeInfo.ListenAddr,
		"enode":           nodeInfo.Enode,
		"id":              nodeInfo.ID,
		"ip":              nodeInfo.IP,
		"ports": map[string]int{
			"discovery": nodeInfo.Ports.Discovery,
			"listener":  nodeInfo.Ports.Listener,
		},
	}
	return printJSON(out)
}

func clientUptime(ctx *cli.Context) error {
	var seconds uint64
	if err := callRPC(ctx, &seconds, "admin_uptime"); err != nil {
		return err
	}
	printScalar(seconds)
	return nil
}

// stringField reads a string field from a decoded JSON object, returning
// "" if absent or not a string. Used to safely sift nullable block fields
// in chaininfo output.
func stringField(m map[string]interface{}, key string) string {
	if m == nil {
		return ""
	}
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

func clientPeers(ctx *cli.Context) error {
	var peers []*p2p.PeerInfo
	if err := callRPC(ctx, &peers, "admin_peers"); err != nil {
		return err
	}
	// Always emit a JSON array, even when empty, matching bitcoin-cli's
	// getpeerinfo (which returns `[]`, not a text message).
	if peers == nil {
		peers = []*p2p.PeerInfo{}
	}
	return printJSON(peers)
}

func clientBlockCount(ctx *cli.Context) error {
	var hex string
	if err := callRPC(ctx, &hex, "eth_blockNumber"); err != nil {
		return err
	}
	printScalar(hexToUint(hex))
	return nil
}

func clientSyncing(ctx *cli.Context) error {
	var raw json.RawMessage
	if err := callRPC(ctx, &raw, "eth_syncing"); err != nil {
		return err
	}
	// bitcoin-cli has no direct "syncing" command but getblockchaininfo
	// exposes `initialblockdownload` and `verificationprogress`. We surface
	// the raw RPC result: `false` when synced, or a JSON progress object.
	if len(raw) == 0 || string(raw) == "false" {
		printScalar(false)
		return nil
	}
	var pretty interface{}
	if err := json.Unmarshal(raw, &pretty); err != nil {
		printScalar(string(raw))
		return nil
	}
	return printJSON(pretty)
}

func clientMempool(ctx *cli.Context) error {
	var status map[string]string
	if err := callRPC(ctx, &status, "txpool_status"); err != nil {
		return err
	}
	// Match getmempoolinfo's JSON-object shape, with numeric values
	// rather than hex strings for script-friendliness.
	out := map[string]uint64{
		"pending": hexToUint(status["pending"]),
		"queued":  hexToUint(status["queued"]),
	}
	return printJSON(out)
}

func clientMempoolContent(ctx *cli.Context) error {
	method := "txpool_content"
	if ctx.Bool("inspect") {
		method = "txpool_inspect"
	}
	var args []interface{}
	if addr := ctx.String("address"); addr != "" {
		// contentFrom / inspectFrom variants accept a single address.
		// Inspect has no "from" variant in vanilla geth; fall back to
		// filtering client-side in that case.
		if method == "txpool_content" {
			method = "txpool_contentFrom"
			args = []interface{}{addr}
		}
	}
	var content json.RawMessage
	if err := callRPC(ctx, &content, method, args...); err != nil {
		return err
	}
	var pretty interface{}
	if err := json.Unmarshal(content, &pretty); err != nil {
		return err
	}
	// If we used --inspect with --address, filter client-side since there
	// is no txpool_inspectFrom RPC.
	if ctx.Bool("inspect") && ctx.String("address") != "" {
		if m, ok := pretty.(map[string]interface{}); ok {
			pretty = filterByAddress(m, ctx.String("address"))
		}
	}
	return printJSON(pretty)
}

// filterByAddress narrows a {pending, queued} map so each bucket contains
// only the entries for the given sender. Address comparison is done
// case-insensitively so 0xABCD… matches 0xabcd… — txpool_inspect emits
// checksummed addresses while users commonly pass lowercase.
func filterByAddress(pool map[string]interface{}, addr string) map[string]interface{} {
	want := strings.ToLower(addr)
	out := make(map[string]interface{}, len(pool))
	for bucket, v := range pool {
		entries, ok := v.(map[string]interface{})
		if !ok {
			out[bucket] = v
			continue
		}
		filtered := make(map[string]interface{})
		for k, val := range entries {
			if strings.ToLower(k) == want {
				filtered[k] = val
			}
		}
		out[bucket] = filtered
	}
	return out
}

func clientEstimateGas(ctx *cli.Context) error {
	to, err := requireArg(ctx, "to")
	if err != nil {
		return err
	}
	// Build the call object eth_estimateGas expects. Omitted fields are
	// left out entirely rather than defaulted to 0x0, because some clients
	// treat an explicit value differently than absence.
	call := map[string]interface{}{"to": to}
	if from := ctx.String("from"); from != "" {
		call["from"] = from
	}
	if value := ctx.String("value"); value != "" {
		call["value"] = toHexAmount(value)
	}
	if data := ctx.String("data"); data != "" {
		if !strings.HasPrefix(data, "0x") {
			data = "0x" + data
		}
		call["data"] = data
	}
	var gasHex string
	if err := callRPC(ctx, &gasHex, "eth_estimateGas", call); err != nil {
		return err
	}
	printScalar(hexToBigInt(gasHex).String())
	return nil
}

// toHexAmount accepts either a decimal integer string or a 0x-prefixed
// hex string and returns the 0x-prefixed hex form eth_* RPCs expect for
// numeric call parameters. Returns "0x0" on parse failure; the RPC will
// surface any resulting error.
func toHexAmount(s string) string {
	if strings.HasPrefix(s, "0x") || strings.HasPrefix(s, "0X") {
		return s
	}
	n, ok := new(big.Int).SetString(s, 10)
	if !ok {
		return "0x0"
	}
	return "0x" + n.Text(16)
}

func clientBalance(ctx *cli.Context) error {
	addr, err := requireArg(ctx, "address")
	if err != nil {
		return err
	}
	var hex string
	if err := callRPC(ctx, &hex, "eth_getBalance", addr, resolveBlockTag(ctx)); err != nil {
		return err
	}
	wei := hexToBigInt(hex)
	if ctx.Bool(weiFlag.Name) {
		printScalar(wei.String())
	} else {
		// Default: decimal LAX string, matching bitcoin-cli's default
		// BTC decimal display (getbalance → `0.12345`).
		printScalar(weiToLAX(wei))
	}
	return nil
}

func clientNonce(ctx *cli.Context) error {
	addr, err := requireArg(ctx, "address")
	if err != nil {
		return err
	}
	var hex string
	if err := callRPC(ctx, &hex, "eth_getTransactionCount", addr, resolveBlockTag(ctx)); err != nil {
		return err
	}
	// Bare integer, matching bitcoin-cli's getreceivedbyaddress output
	// style (plain number for counters).
	printScalar(hexToBigInt(hex).String())
	return nil
}

func clientCode(ctx *cli.Context) error {
	addr, err := requireArg(ctx, "address")
	if err != nil {
		return err
	}
	var code string
	if err := callRPC(ctx, &code, "eth_getCode", addr, resolveBlockTag(ctx)); err != nil {
		return err
	}
	// EOAs return "0x"; contracts return their bytecode. Print the raw
	// hex either way so shell pipelines can grep "^0x$" to branch.
	printScalar(code)
	return nil
}

func clientStorage(ctx *cli.Context) error {
	addr, err := requireArg(ctx, "address")
	if err != nil {
		return err
	}
	if ctx.NArg() < 2 {
		return fmt.Errorf("missing required argument: slot")
	}
	slot := ctx.Args().Get(1)
	var value string
	if err := callRPC(ctx, &value, "eth_getStorageAt", addr, toHexAmount(slot), resolveBlockTag(ctx)); err != nil {
		return err
	}
	printScalar(value)
	return nil
}

// resolveBlockTag returns the block identifier for account-state RPC
// calls. Accepts the named tags unchanged, and converts decimal numbers
// to 0x-hex because eth_* RPCs refuse decimal block parameters.
func resolveBlockTag(ctx *cli.Context) string {
	tag := ctx.String(blockTagFlag.Name)
	if tag == "" {
		return "latest"
	}
	switch tag {
	case "latest", "earliest", "pending", "safe", "finalized":
		return tag
	}
	if strings.HasPrefix(tag, "0x") || strings.HasPrefix(tag, "0X") {
		return tag
	}
	if n, ok := new(big.Int).SetString(tag, 10); ok {
		return "0x" + n.Text(16)
	}
	// Pass it through unchanged; the RPC will surface a clear error.
	return tag
}

func clientGetTx(ctx *cli.Context) error {
	hash, err := requireArg(ctx, "txhash")
	if err != nil {
		return err
	}
	var tx json.RawMessage
	if err := callRPC(ctx, &tx, "eth_getTransactionByHash", hash); err != nil {
		return err
	}
	if len(tx) == 0 || string(tx) == "null" {
		return fmt.Errorf("transaction %s not found", hash)
	}
	var pretty interface{}
	if err := json.Unmarshal(tx, &pretty); err != nil {
		return err
	}
	return printJSON(pretty)
}

func clientGetReceipt(ctx *cli.Context) error {
	hash, err := requireArg(ctx, "txhash")
	if err != nil {
		return err
	}
	var rcpt json.RawMessage
	if err := callRPC(ctx, &rcpt, "eth_getTransactionReceipt", hash); err != nil {
		return err
	}
	if len(rcpt) == 0 || string(rcpt) == "null" {
		return fmt.Errorf("receipt for %s not found (is the transaction mined?)", hash)
	}
	var pretty interface{}
	if err := json.Unmarshal(rcpt, &pretty); err != nil {
		return err
	}
	return printJSON(pretty)
}

func clientGetBlock(ctx *cli.Context) error {
	id, err := requireArg(ctx, "number|hash")
	if err != nil {
		return err
	}
	byHash, param, err := resolveBlockID(id)
	if err != nil {
		return err
	}
	method := "eth_getBlockByNumber"
	if byHash {
		method = "eth_getBlockByHash"
	}
	var block json.RawMessage
	if err := callRPC(ctx, &block, method, param, ctx.Bool("full")); err != nil {
		return err
	}
	if len(block) == 0 || string(block) == "null" {
		return fmt.Errorf("block %s not found", id)
	}
	var pretty interface{}
	if err := json.Unmarshal(block, &pretty); err != nil {
		return err
	}
	return printJSON(pretty)
}

func clientGetBlockHash(ctx *cli.Context) error {
	id, err := requireArg(ctx, "number")
	if err != nil {
		return err
	}
	// Refuse hashes here — getblockhash maps height → hash, not the other
	// way around. Bitcoin's getblockhash behaves the same way.
	if strings.HasPrefix(id, "0x") && len(id) == 66 {
		return fmt.Errorf("getblockhash takes a block number, not a hash (did you mean `getheader`?)")
	}
	_, param, err := resolveBlockID(id)
	if err != nil {
		return err
	}
	var block map[string]interface{}
	if err := callRPC(ctx, &block, "eth_getBlockByNumber", param, false); err != nil {
		return err
	}
	if block == nil {
		return fmt.Errorf("block %s not found", id)
	}
	hash, _ := block["hash"].(string)
	if hash == "" {
		return fmt.Errorf("block %s has no hash (pending?)", id)
	}
	printScalar(hash)
	return nil
}

func clientGetHeader(ctx *cli.Context) error {
	id, err := requireArg(ctx, "number|hash")
	if err != nil {
		return err
	}
	byHash, param, err := resolveBlockID(id)
	if err != nil {
		return err
	}
	method := "eth_getHeaderByNumber"
	if byHash {
		method = "eth_getHeaderByHash"
	}
	var header json.RawMessage
	if err := callRPC(ctx, &header, method, param); err != nil {
		return err
	}
	if len(header) == 0 || string(header) == "null" {
		return fmt.Errorf("header %s not found", id)
	}
	var pretty interface{}
	if err := json.Unmarshal(header, &pretty); err != nil {
		return err
	}
	return printJSON(pretty)
}

func clientTip(ctx *cli.Context) error {
	var block map[string]interface{}
	if err := callRPC(ctx, &block, "eth_getBlockByNumber", "latest", false); err != nil {
		return err
	}
	if block == nil {
		return fmt.Errorf("no chain tip available")
	}
	// Condense the block object into the fields operators most often
	// want at a glance. Full details remain one `getblock latest` away.
	txs, _ := block["transactions"].([]interface{})
	out := map[string]interface{}{
		"number":     hexToUint(stringField(block, "number")),
		"hash":       stringField(block, "hash"),
		"parent":     stringField(block, "parentHash"),
		"timestamp":  hexToUint(stringField(block, "timestamp")),
		"miner":      stringField(block, "miner"),
		"gasused":    hexToUint(stringField(block, "gasUsed")),
		"gaslimit":   hexToUint(stringField(block, "gasLimit")),
		"difficulty": stringField(block, "difficulty"),
		"size":       hexToUint(stringField(block, "size")),
		"txs":        len(txs),
	}
	return printJSON(out)
}

// resolveBlockID parses a user-supplied block identifier and reports
// whether the hash-keyed or number-keyed RPC should be used. It accepts:
//   - 0x-prefixed 32-byte hex hashes (byHash=true),
//   - the tags latest/earliest/pending/safe/finalized (passed through),
//   - decimal block numbers (converted to 0x-hex).
//
// Returns (byHash, param, err). Used by getblock, getblockhash, getheader.
func resolveBlockID(id string) (byHash bool, param string, err error) {
	switch {
	case strings.HasPrefix(id, "0x") && len(id) == 66:
		return true, id, nil
	case id == "latest" || id == "earliest" || id == "pending" || id == "finalized" || id == "safe":
		return false, id, nil
	default:
		n, ok := new(big.Int).SetString(id, 10)
		if !ok {
			return false, "", fmt.Errorf("invalid block number or hash: %s", id)
		}
		return false, "0x" + n.Text(16), nil
	}
}

func clientGasPrice(ctx *cli.Context) error {
	var hex string
	if err := callRPC(ctx, &hex, "eth_gasPrice"); err != nil {
		return err
	}
	// Bare wei integer, matching the scalar-return convention used for
	// amount-like values (getblockcount, getbalance).
	printScalar(hexToBigInt(hex).String())
	return nil
}

func clientSendRaw(ctx *cli.Context) error {
	hex, err := requireArg(ctx, "hex")
	if err != nil {
		return err
	}
	if !strings.HasPrefix(hex, "0x") {
		hex = "0x" + hex
	}
	var txHash string
	if err := callRPC(ctx, &txHash, "eth_sendRawTransaction", hex); err != nil {
		return err
	}
	// bitcoin-cli sendrawtransaction prints the bare txid. Match.
	printScalar(txHash)
	return nil
}

func clientAddPeer(ctx *cli.Context) error {
	enode, err := requireArg(ctx, "enode")
	if err != nil {
		return err
	}
	var ok bool
	if err := callRPC(ctx, &ok, "admin_addPeer", enode); err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("admin_addPeer returned false")
	}
	// bitcoin-cli addnode prints nothing on success.
	return nil
}

func clientRemovePeer(ctx *cli.Context) error {
	enode, err := requireArg(ctx, "enode")
	if err != nil {
		return err
	}
	var ok bool
	if err := callRPC(ctx, &ok, "admin_removePeer", enode); err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("admin_removePeer returned false")
	}
	return nil
}
