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

	balanceCommand = cli.Command{
		Action:    utils.MigrateFlags(clientBalance),
		Name:      "balance",
		Usage:     "Print the balance of an account",
		ArgsUsage: "<address>",
		Flags:     append([]cli.Flag{weiFlag}, clientCommandFlags...),
		Category:  "CLIENT COMMANDS",
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
	peersCommand,
	blockCountCommand,
	syncingCommand,
	mempoolCommand,
	balanceCommand,
	getTxCommand,
	getReceiptCommand,
	getBlockCommand,
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

func clientBalance(ctx *cli.Context) error {
	addr, err := requireArg(ctx, "address")
	if err != nil {
		return err
	}
	var hex string
	if err := callRPC(ctx, &hex, "eth_getBalance", addr, "latest"); err != nil {
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
	full := ctx.Bool("full")

	// eth_getBlockByNumber / eth_getBlockByHash share the same response
	// shape; pick the right method based on whether id looks like a hash.
	method := "eth_getBlockByNumber"
	param := id
	switch {
	case strings.HasPrefix(id, "0x") && len(id) == 66:
		method = "eth_getBlockByHash"
	case id == "latest" || id == "earliest" || id == "pending" || id == "finalized" || id == "safe":
		// keep as-is
	default:
		// numeric: convert to hex
		n, ok := new(big.Int).SetString(id, 10)
		if !ok {
			return fmt.Errorf("invalid block number or hash: %s", id)
		}
		param = "0x" + n.Text(16)
	}

	var block json.RawMessage
	if err := callRPC(ctx, &block, method, param, full); err != nil {
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
