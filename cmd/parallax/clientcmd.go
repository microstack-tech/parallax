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
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/ParallaxProtocol/parallax/cmd/utils"
	"github.com/ParallaxProtocol/parallax/crypto"
	"github.com/ParallaxProtocol/parallax/p2p"
	"github.com/ParallaxProtocol/parallax/primitives/types"
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

// passwordFileFlag lets the wallet commands read a passphrase from a file
// instead of prompting. Only the first line of the file is used. Matches
// bitcoin-cli's -stdinrpcpass / -rpcwallet pattern in spirit — keep the
// secret out of the process table (never pass it as a CLI arg).
var passwordFileFlag = cli.StringFlag{
	Name:  "password",
	Usage: "Read the passphrase from this file (first line). Omit to prompt on stdin.",
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

	addTrustedCommand = cli.Command{
		Action:    utils.MigrateFlags(clientAddTrusted),
		Name:      "addtrusted",
		Usage:     "Mark a peer as trusted (bypasses connection slot limits)",
		ArgsUsage: "<enode>",
		Flags:     clientCommandFlags,
		Category:  "CLIENT COMMANDS",
		Description: `
Adds the peer to the trusted list so it is always allowed to connect,
even when the node is at its max-peers limit. Uses admin_addTrustedPeer.`,
	}

	miningCommand = cli.Command{
		Action:    utils.MigrateFlags(clientMining),
		Name:      "mining",
		Usage:     "Show mining status (enabled, hashrate, coinbase)",
		ArgsUsage: " ",
		Flags:     clientCommandFlags,
		Category:  "CLIENT COMMANDS",
		Description: `
Equivalent to bitcoin-cli getmininginfo. Returns a JSON object summarising
the miner state: whether mining is enabled, current hashrate, configured
coinbase, and the latest canonical block's number/timestamp for context.`,
	}

	startMiningCommand = cli.Command{
		Action:    utils.MigrateFlags(clientStartMining),
		Name:      "startmining",
		Usage:     "Start the miner",
		ArgsUsage: "[threads]",
		Flags:     clientCommandFlags,
		Category:  "CLIENT COMMANDS",
		Description: `
Starts the built-in miner. Optional positional argument specifies the
number of CPU threads; omit it to use all logical CPUs. Uses miner_start.`,
	}

	stopMiningCommand = cli.Command{
		Action:    utils.MigrateFlags(clientStopMining),
		Name:      "stopmining",
		Usage:     "Stop the miner",
		ArgsUsage: " ",
		Flags:     clientCommandFlags,
		Category:  "CLIENT COMMANDS",
		Description: `
Stops the built-in miner. Silent on success. Uses miner_stop.`,
	}

	setCoinbaseCommand = cli.Command{
		Action:    utils.MigrateFlags(clientSetCoinbase),
		Name:      "setcoinbase",
		Usage:     "Set the address that receives mining rewards",
		ArgsUsage: "<address>",
		Flags:     clientCommandFlags,
		Category:  "CLIENT COMMANDS",
		Description: `
Updates the coinbase (the address credited with block rewards) without
restarting the node. Silent on success. Uses miner_setCoinbase.`,
	}

	logLevelCommand = cli.Command{
		Action:    utils.MigrateFlags(clientLogLevel),
		Name:      "loglevel",
		Usage:     "Set log verbosity at runtime (integer level or vmodule pattern)",
		ArgsUsage: "<level|pattern>",
		Flags:     clientCommandFlags,
		Category:  "CLIENT COMMANDS",
		Description: `
Accepts either an integer level (0=silent, 1=error, 2=warn, 3=info,
4=debug, 5=detail) or a vmodule pattern like "eth/*=5,p2p=4".
Equivalent to bitcoin-cli's logging command. Uses debug_verbosity or
debug_vmodule depending on the argument shape.`,
	}

	traceCommand = cli.Command{
		Action:    utils.MigrateFlags(clientTrace),
		Name:      "trace",
		Usage:     "Trace the execution of a mined transaction",
		ArgsUsage: "<txhash>",
		Flags: append([]cli.Flag{
			cli.StringFlag{Name: "tracer", Usage: "Named tracer (e.g. callTracer, prestateTracer, 4byteTracer); default returns opcode-level steps"},
			cli.StringFlag{Name: "timeout", Usage: "RPC timeout as a Go duration (e.g. 30s, 2m); default 5m because traces can be slow"},
		}, clientCommandFlags...),
		Category: "CLIENT COMMANDS",
		Description: `
Calls debug_traceTransaction and prints the trace as JSON. Without
--tracer the output is the full opcode-by-opcode trace (can be huge);
pass a named tracer for a condensed view. Use --timeout to raise the
client deadline for long-running traces.`,
	}

	listAccountsCommand = cli.Command{
		Action:    utils.MigrateFlags(clientListAccounts),
		Name:      "listaccounts",
		Usage:     "List accounts the running node can see",
		ArgsUsage: " ",
		Flags:     clientCommandFlags,
		Category:  "CLIENT COMMANDS",
		Description: `
Calls personal_listAccounts and prints a JSON array of addresses.
Equivalent to the offline "parallax account list" but works against a
running daemon without stopping it.`,
	}

	newAccountCommand = cli.Command{
		Action:    utils.MigrateFlags(clientNewAccount),
		Name:      "newaccount",
		Usage:     "Create a new account in the running node's keystore",
		ArgsUsage: " ",
		Flags: append([]cli.Flag{
			passwordFileFlag,
		}, clientCommandFlags...),
		Category: "CLIENT COMMANDS",
		Description: `
Prompts for a password (with confirmation) and asks the node to create
a new keystore entry via personal_newAccount. Prints the bare address
on success. Use --password to read the passphrase from a file instead
of prompting; the first line of the file is used.`,
	}

	unlockAccountCommand = cli.Command{
		Action:    utils.MigrateFlags(clientUnlockAccount),
		Name:      "unlock",
		Usage:     "Unlock an account on the running node",
		ArgsUsage: "<address>",
		Flags: append([]cli.Flag{
			passwordFileFlag,
			cli.DurationFlag{
				Name:  "duration",
				Usage: "How long to keep the account unlocked (e.g. 5m, 1h); 0 = until the node stops. Default 5m.",
			},
		}, clientCommandFlags...),
		Category: "CLIENT COMMANDS",
		Description: `
Calls personal_unlockAccount. Equivalent to bitcoin-cli walletpassphrase.
Silent on success. Prompts for the passphrase unless --password is set.`,
	}

	lockAccountCommand = cli.Command{
		Action:    utils.MigrateFlags(clientLockAccount),
		Name:      "lock",
		Usage:     "Lock a previously unlocked account",
		ArgsUsage: "<address>",
		Flags:     clientCommandFlags,
		Category:  "CLIENT COMMANDS",
		Description: `
Calls personal_lockAccount. Equivalent to bitcoin-cli walletlock.
Silent on success.`,
	}

	signCommand = cli.Command{
		Action:    utils.MigrateFlags(clientSign),
		Name:      "sign",
		Usage:     "Sign data with an account (Ethereum personal_sign)",
		ArgsUsage: "<address> <data>",
		Flags: append([]cli.Flag{
			passwordFileFlag,
		}, clientCommandFlags...),
		Category: "CLIENT COMMANDS",
		Description: `
Calls personal_sign with the Ethereum text-message prefix. <data>
accepts a plain UTF-8 string or a 0x-prefixed hex blob (auto-detected).
Prints the signature as a 0x-prefixed hex string. Prompts for the
passphrase unless --password is set.`,
	}

	decodeRawCommand = cli.Command{
		Action:    utils.MigrateFlags(clientDecodeRaw),
		Name:      "decoderaw",
		Usage:     "Decode a raw signed transaction (offline, no daemon required)",
		ArgsUsage: "<hex>",
		Category:  "CLIENT COMMANDS",
		Description: `
Parses a 0x-prefixed RLP-encoded signed transaction locally and prints
its fields as JSON, including the recovered sender. Does not contact
the running node — runs entirely client-side, analogous to
bitcoin-cli's decoderawtransaction / bitcoin-tx decodetx.`,
	}

	toAddrCommand = cli.Command{
		Action:    utils.MigrateFlags(clientToAddr),
		Name:      "toaddr",
		Usage:     "Derive the address for a private key (offline, no daemon required)",
		ArgsUsage: "<privkey-hex>",
		Category:  "CLIENT COMMANDS",
		Description: `
Derives and prints the 0x-address for a hex-encoded secp256k1 private
key. Runs entirely client-side: the key is never sent to any node or
written to disk. Useful when verifying a backup or wiring up a key
before funding it.`,
	}

	sendTxCommand = cli.Command{
		Action:    utils.MigrateFlags(clientSendTx),
		Name:      "sendtx",
		Usage:     "Build, sign and send a transaction from an unlocked account",
		ArgsUsage: " ",
		Flags: append([]cli.Flag{
			cli.StringFlag{Name: "from", Usage: "Sender address (required)"},
			cli.StringFlag{Name: "to", Usage: "Recipient address; omit to deploy a contract"},
			cli.StringFlag{Name: "value", Usage: "Value in wei (integer or 0x hex); default 0"},
			cli.StringFlag{Name: "data", Usage: "Call data as hex (0x-prefixed); default empty"},
			cli.StringFlag{Name: "gas", Usage: "Gas limit (integer); let the node estimate if omitted"},
			cli.StringFlag{Name: "gasprice", Usage: "Gas price in wei; let the node default if omitted"},
			cli.StringFlag{Name: "nonce", Usage: "Explicit nonce; looked up automatically if omitted"},
			passwordFileFlag,
		}, clientCommandFlags...),
		Category: "CLIENT COMMANDS",
		Description: `
Builds a transaction from flags and signs+submits it via
personal_sendTransaction. Prints the transaction hash on success.
Prompts for the passphrase unless --password is set.`,
	}

	dbStatsCommand = cli.Command{
		Action:    utils.MigrateFlags(clientDbStats),
		Name:      "dbstats",
		Usage:     "Show chaindata and ancient database sizes",
		ArgsUsage: " ",
		Flags:     clientCommandFlags,
		Category:  "CLIENT COMMANDS",
		Description: `
Summary of on-disk database state: per-category ancient-freezer sizes
(bytes) and the LevelDB compaction stats table. Uses debug_dbStats.`,
	}

	setExtraCommand = cli.Command{
		Action:    utils.MigrateFlags(clientSetExtra),
		Name:      "setextra",
		Usage:     "Set the extra-data field mined blocks will carry",
		ArgsUsage: "<data>",
		Flags:     clientCommandFlags,
		Category:  "CLIENT COMMANDS",
		Description: `
Updates the extra-data bytes embedded in blocks this node mines.
Accepts a plain string or a 0x-prefixed hex blob (auto-detected).
Must fit the 32-byte extradata limit — longer values are rejected by
the miner. Uses miner_setExtra.`,
	}

	removeTrustedCommand = cli.Command{
		Action:    utils.MigrateFlags(clientRemoveTrusted),
		Name:      "removetrusted",
		Usage:     "Remove a peer from the trusted list",
		ArgsUsage: "<enode>",
		Flags:     clientCommandFlags,
		Category:  "CLIENT COMMANDS",
		Description: `
Removes the peer from the trusted list. The peer is not disconnected
automatically; use removepeer for that. Uses admin_removeTrustedPeer.`,
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
	addTrustedCommand,
	removeTrustedCommand,
	miningCommand,
	startMiningCommand,
	stopMiningCommand,
	setCoinbaseCommand,
	setExtraCommand,
	logLevelCommand,
	traceCommand,
	dbStatsCommand,
	listAccountsCommand,
	newAccountCommand,
	unlockAccountCommand,
	lockAccountCommand,
	signCommand,
	sendTxCommand,
	decodeRawCommand,
	toAddrCommand,
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
	return callPeerAdmin(ctx, "admin_addPeer", "enode|host:port")
}

func clientRemovePeer(ctx *cli.Context) error {
	return callPeerAdmin(ctx, "admin_removePeer", "enode|host:port")
}

func clientAddTrusted(ctx *cli.Context) error {
	return callPeerAdmin(ctx, "admin_addTrustedPeer", "enode|host:port")
}

func clientRemoveTrusted(ctx *cli.Context) error {
	return callPeerAdmin(ctx, "admin_removeTrustedPeer", "enode|host:port")
}

// callPeerAdmin implements the shared body of all four peer-admin sugar
// commands. It reads the first positional argument, resolves it to a full
// enode URL if the user gave a bare host:port, and dispatches the named
// admin_* RPC. The RPCs all return (bool, error) with false-meaning-no-op
// semantics, so we raise that to an error for script-friendliness.
// readPassword returns the passphrase to use for a personal_* call. If
// --password points at a file, the first non-empty line is used; otherwise
// the user is prompted on stdin/tty. Confirmation (double-entry) is used
// for newaccount where a typo would leave an unrecoverable keystore file.
func readPassword(ctx *cli.Context, prompt string, confirm bool) (string, error) {
	if file := ctx.String(passwordFileFlag.Name); file != "" {
		data, err := os.ReadFile(file)
		if err != nil {
			return "", fmt.Errorf("read password file: %v", err)
		}
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimRight(line, "\r")
			if line != "" {
				return line, nil
			}
		}
		return "", fmt.Errorf("password file %s is empty", file)
	}
	// Prompt interactively. Reuse the same terminal prompter the offline
	// account commands use so the UX is consistent.
	return utils.GetPassPhrase(prompt, confirm), nil
}

func clientListAccounts(ctx *cli.Context) error {
	var accounts []string
	if err := callRPC(ctx, &accounts, "personal_listAccounts"); err != nil {
		return err
	}
	if accounts == nil {
		accounts = []string{}
	}
	return printJSON(accounts)
}

func clientNewAccount(ctx *cli.Context) error {
	pwd, err := readPassword(ctx, "Your new account is locked with a password. Please give a password. Do not forget this password.", true)
	if err != nil {
		return err
	}
	var addr string
	if err := callRPC(ctx, &addr, "personal_newAccount", pwd); err != nil {
		return err
	}
	printScalar(addr)
	return nil
}

func clientUnlockAccount(ctx *cli.Context) error {
	addr, err := requireArg(ctx, "address")
	if err != nil {
		return err
	}
	pwd, err := readPassword(ctx, fmt.Sprintf("Unlock account %s", addr), false)
	if err != nil {
		return err
	}
	// The RPC accepts *uint64 seconds. Default to 300s (matching geth's
	// built-in default for personal_unlockAccount) when --duration is 0
	// so a bare "parallax unlock" doesn't silently keep the key available
	// forever.
	seconds := uint64(300)
	if d := ctx.Duration("duration"); d > 0 {
		seconds = uint64(d.Seconds())
	} else if ctx.IsSet("duration") {
		// Explicit --duration 0 — preserve geth's "until node stops"
		// semantics by passing a null duration.
		var ok bool
		if err := callRPC(ctx, &ok, "personal_unlockAccount", addr, pwd, nil); err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("personal_unlockAccount returned false")
		}
		return nil
	}
	var ok bool
	if err := callRPC(ctx, &ok, "personal_unlockAccount", addr, pwd, seconds); err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("personal_unlockAccount returned false")
	}
	return nil
}

func clientLockAccount(ctx *cli.Context) error {
	addr, err := requireArg(ctx, "address")
	if err != nil {
		return err
	}
	var ok bool
	if err := callRPC(ctx, &ok, "personal_lockAccount", addr); err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("personal_lockAccount returned false")
	}
	return nil
}

func clientSign(ctx *cli.Context) error {
	addr, err := requireArg(ctx, "address")
	if err != nil {
		return err
	}
	if ctx.NArg() < 2 {
		return fmt.Errorf("missing required argument: data")
	}
	data := ctx.Args().Get(1)
	// personal_sign expects 0x-prefixed hex bytes. A plain UTF-8 string
	// gets hex-encoded client-side so users can pass either form without
	// thinking about encoding.
	hexData := data
	if !(strings.HasPrefix(data, "0x") || strings.HasPrefix(data, "0X")) {
		hexData = "0x" + hexEncode([]byte(data))
	}
	pwd, err := readPassword(ctx, fmt.Sprintf("Sign with %s", addr), false)
	if err != nil {
		return err
	}
	var sig string
	if err := callRPC(ctx, &sig, "personal_sign", hexData, addr, pwd); err != nil {
		return err
	}
	printScalar(sig)
	return nil
}

func clientSendTx(ctx *cli.Context) error {
	from := ctx.String("from")
	if from == "" {
		return fmt.Errorf("--from is required")
	}
	args := map[string]interface{}{"from": from}
	if to := ctx.String("to"); to != "" {
		args["to"] = to
	}
	if v := ctx.String("value"); v != "" {
		args["value"] = toHexAmount(v)
	}
	if d := ctx.String("data"); d != "" {
		if !strings.HasPrefix(d, "0x") {
			d = "0x" + d
		}
		args["data"] = d
	}
	if g := ctx.String("gas"); g != "" {
		args["gas"] = toHexAmount(g)
	}
	if gp := ctx.String("gasprice"); gp != "" {
		args["gasPrice"] = toHexAmount(gp)
	}
	if n := ctx.String("nonce"); n != "" {
		args["nonce"] = toHexAmount(n)
	}
	pwd, err := readPassword(ctx, fmt.Sprintf("Send transaction from %s", from), false)
	if err != nil {
		return err
	}
	var hash string
	if err := callRPC(ctx, &hash, "personal_sendTransaction", args, pwd); err != nil {
		return err
	}
	printScalar(hash)
	return nil
}

func clientDecodeRaw(ctx *cli.Context) error {
	hexStr, err := requireArg(ctx, "hex")
	if err != nil {
		return err
	}
	hexStr = strings.TrimPrefix(strings.TrimPrefix(hexStr, "0x"), "0X")
	raw, err := hexDecode(hexStr)
	if err != nil {
		return fmt.Errorf("invalid hex: %v", err)
	}
	var tx types.Transaction
	if err := tx.UnmarshalBinary(raw); err != nil {
		return fmt.Errorf("decode transaction: %v", err)
	}

	// Recover sender. ChainId is zero for pre-EIP-155 legacy txs; in
	// that case LatestSignerForChainID still returns a usable signer.
	chainID := tx.ChainId()
	if chainID == nil {
		chainID = new(big.Int)
	}
	signer := types.LatestSignerForChainID(chainID)
	from, err := types.Sender(signer, &tx)
	fromStr := ""
	if err == nil {
		fromStr = from.Hex()
	}

	v, r, s := tx.RawSignatureValues()
	out := map[string]interface{}{
		"hash":     tx.Hash().Hex(),
		"type":     tx.Type(),
		"chainid":  chainID.String(),
		"nonce":    tx.Nonce(),
		"value":    tx.Value().String(),
		"gas":      tx.Gas(),
		"gasprice": tx.GasPrice().String(),
		"data":     "0x" + hexEncode(tx.Data()),
		"v":        v.String(),
		"r":        r.String(),
		"s":        s.String(),
	}
	if fromStr != "" {
		out["from"] = fromStr
	} else {
		out["from"] = nil
	}
	if to := tx.To(); to != nil {
		out["to"] = to.Hex()
	} else {
		out["to"] = nil // contract creation
	}
	// Dynamic-fee fields only mean anything for EIP-1559 txs. Legacy
	// txs synthesise GasTipCap/GasFeeCap by returning gasPrice for
	// compatibility — emitting those under 1559-specific keys would
	// mislead readers, so skip them unless the tx is actually typed.
	if tx.Type() == types.DynamicFeeTxType {
		if tip := tx.GasTipCap(); tip != nil {
			out["maxpriorityfeepergas"] = tip.String()
		}
		if cap := tx.GasFeeCap(); cap != nil {
			out["maxfeepergas"] = cap.String()
		}
	}
	return printJSON(out)
}

func clientToAddr(ctx *cli.Context) error {
	key, err := requireArg(ctx, "privkey-hex")
	if err != nil {
		return err
	}
	key = strings.TrimPrefix(strings.TrimPrefix(key, "0x"), "0X")
	priv, err := crypto.HexToECDSA(key)
	if err != nil {
		return fmt.Errorf("invalid private key: %v", err)
	}
	addr := crypto.PubkeyToAddress(priv.PublicKey)
	printScalar(addr.Hex())
	return nil
}

// hexEncode is the complement of hexDecode: byte slice → lowercase hex
// string without the 0x prefix. Used for encoding plain-text sign payloads
// that personal_sign expects as hex bytes.
func hexEncode(b []byte) string {
	const hexdigits = "0123456789abcdef"
	out := make([]byte, len(b)*2)
	for i, v := range b {
		out[i*2] = hexdigits[v>>4]
		out[i*2+1] = hexdigits[v&0x0f]
	}
	return string(out)
}

func clientLogLevel(ctx *cli.Context) error {
	arg, err := requireArg(ctx, "level|pattern")
	if err != nil {
		return err
	}
	// Heuristic: an integer in [0,5] is a verbosity level; anything else
	// (e.g. "eth/*=5" or "p2p=4") is a vmodule pattern.
	if n, err := strconv.Atoi(arg); err == nil && n >= 0 && n <= 5 {
		var result json.RawMessage
		if err := callRPC(ctx, &result, "debug_verbosity", n); err != nil {
			return err
		}
		return nil
	}
	var result json.RawMessage
	if err := callRPC(ctx, &result, "debug_vmodule", arg); err != nil {
		return err
	}
	return nil
}

func clientTrace(ctx *cli.Context) error {
	hash, err := requireArg(ctx, "txhash")
	if err != nil {
		return err
	}
	// Traces can be slow and huge; give them a generous default deadline.
	timeout := 5 * time.Minute
	if raw := ctx.String("timeout"); raw != "" {
		d, err := time.ParseDuration(raw)
		if err != nil {
			return fmt.Errorf("invalid --timeout %q: %v", raw, err)
		}
		timeout = d
	}

	client, _, err := dialClient(ctx)
	if err != nil {
		return err
	}
	defer client.Close()

	cctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	// Build the tracer-config object only if a tracer was requested; an
	// absent second param makes debug_traceTransaction return the
	// opcode-level trace.
	var params []interface{}
	params = append(params, hash)
	if tracer := ctx.String("tracer"); tracer != "" {
		params = append(params, map[string]interface{}{"tracer": tracer})
	}

	var raw json.RawMessage
	if err := client.CallContext(cctx, &raw, "debug_traceTransaction", params...); err != nil {
		return err
	}
	if len(raw) == 0 || string(raw) == "null" {
		return fmt.Errorf("no trace returned for %s (is the transaction mined?)", hash)
	}
	var pretty interface{}
	if err := json.Unmarshal(raw, &pretty); err != nil {
		return err
	}
	return printJSON(pretty)
}

func clientDbStats(ctx *cli.Context) error {
	var stats map[string]interface{}
	if err := callRPC(ctx, &stats, "debug_dbStats"); err != nil {
		return err
	}
	return printJSON(stats)
}

func clientMining(ctx *cli.Context) error {
	client, _, err := dialClient(ctx)
	if err != nil {
		return err
	}
	defer client.Close()
	cctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var (
		mining       bool
		hashrateHex  string
		coinbase     string
		coinbaseErr  error
		blockHex     string
		blockHashPtr map[string]interface{}
	)
	_ = client.CallContext(cctx, &mining, "eth_mining")
	_ = client.CallContext(cctx, &hashrateHex, "eth_hashrate")
	coinbaseErr = client.CallContext(cctx, &coinbase, "eth_coinbase")
	_ = client.CallContext(cctx, &blockHex, "eth_blockNumber")
	_ = client.CallContext(cctx, &blockHashPtr, "eth_getBlockByNumber", "latest", false)

	out := map[string]interface{}{
		"mining":   mining,
		"hashrate": hexToBigInt(hashrateHex).String(),
		"blocks":   hexToUint(blockHex),
	}
	// eth_coinbase errors when no coinbase is configured (e.g. fresh dev
	// node without --miner.coinbase). Surface that as null rather than
	// failing the whole command.
	if coinbaseErr == nil {
		out["coinbase"] = coinbase
	} else {
		out["coinbase"] = nil
	}
	// Include a small hint about the tip so operators can sanity-check
	// that "mining" actually produces blocks without a follow-up call.
	if blockHashPtr != nil {
		out["bestblockhash"] = stringField(blockHashPtr, "hash")
		out["bestblocktime"] = hexToUint(stringField(blockHashPtr, "timestamp"))
	}
	return printJSON(out)
}

func clientStartMining(ctx *cli.Context) error {
	args := ctx.Args()
	var params []interface{}
	if args.Present() {
		n, err := strconv.Atoi(args.First())
		if err != nil {
			return fmt.Errorf("invalid threads value %q: %v", args.First(), err)
		}
		params = []interface{}{n}
	}
	// miner_start returns error only on failure; ignore the zero-value
	// response decoded into a typed placeholder.
	var result json.RawMessage
	if err := callRPC(ctx, &result, "miner_start", params...); err != nil {
		return err
	}
	return nil
}

func clientStopMining(ctx *cli.Context) error {
	var result json.RawMessage
	if err := callRPC(ctx, &result, "miner_stop"); err != nil {
		return err
	}
	return nil
}

func clientSetCoinbase(ctx *cli.Context) error {
	addr, err := requireArg(ctx, "address")
	if err != nil {
		return err
	}
	var ok bool
	if err := callRPC(ctx, &ok, "miner_setCoinbase", addr); err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("miner_setCoinbase returned false")
	}
	return nil
}

func clientSetExtra(ctx *cli.Context) error {
	data, err := requireArg(ctx, "data")
	if err != nil {
		return err
	}
	// Accept either a plain string ("my pool v1") or a 0x-prefixed hex
	// blob. The miner's SetExtra wants raw bytes as a Go string, so we
	// decode hex client-side to avoid double-encoding.
	payload := data
	if strings.HasPrefix(data, "0x") || strings.HasPrefix(data, "0X") {
		raw, err := hexDecode(data[2:])
		if err != nil {
			return fmt.Errorf("invalid hex for extra data: %v", err)
		}
		payload = string(raw)
	}
	var ok bool
	if err := callRPC(ctx, &ok, "miner_setExtra", payload); err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("miner_setExtra returned false")
	}
	return nil
}

// hexDecode is a small wrapper around hex decoding that tolerates odd
// lengths by left-padding with a leading zero — matches how people
// casually pass short values like "0x1" for convenience.
func hexDecode(s string) ([]byte, error) {
	if len(s)%2 == 1 {
		s = "0" + s
	}
	out := make([]byte, len(s)/2)
	for i := 0; i < len(out); i++ {
		var v byte
		for j := 0; j < 2; j++ {
			c := s[i*2+j]
			v <<= 4
			switch {
			case c >= '0' && c <= '9':
				v |= c - '0'
			case c >= 'a' && c <= 'f':
				v |= c - 'a' + 10
			case c >= 'A' && c <= 'F':
				v |= c - 'A' + 10
			default:
				return nil, fmt.Errorf("invalid hex character %q", c)
			}
		}
		out[i] = v
	}
	return out, nil
}

func callPeerAdmin(ctx *cli.Context, method, argName string) error {
	input, err := requireArg(ctx, argName)
	if err != nil {
		return err
	}
	enode, err := resolvePeerTarget(ctx, input)
	if err != nil {
		return err
	}
	var ok bool
	if err := callRPC(ctx, &ok, method, enode); err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("%s returned false", method)
	}
	return nil
}

// resolvePeerTarget normalises a user-supplied peer identifier into the
// full enode URL that the admin_* peer RPCs require.
//
// Accepted inputs:
//   - enode://<64-hex-id>@host:port[?discport=…]  — passed through
//   - enr:<base64>                                 — passed through
//   - host:port                                    — resolved against the
//     running node's current peer list by matching IP and listen port.
//   - host                                         — as above, but without
//     the port constraint; must uniquely identify a connected peer.
//
// The host forms only work when the target peer is currently connected
// (so its cryptographic node ID is known to us). If we cannot identify
// it, we fail with a message pointing the user at the full enode URL
// form. This is intentionally restrictive: adopting an arbitrary pubkey
// discovered by dialling a host would let any process at that address
// get itself trusted.
func resolvePeerTarget(ctx *cli.Context, input string) (string, error) {
	if strings.HasPrefix(input, "enode://") || strings.HasPrefix(input, "enr:") {
		return input, nil
	}

	// Split host[:port]. A missing-port error isn't fatal here — we
	// support both "ip:port" and bare "ip" forms.
	host, port, err := net.SplitHostPort(input)
	if err != nil {
		if strings.Contains(err.Error(), "missing port") {
			host, port = input, ""
		} else {
			return "", fmt.Errorf("invalid peer identifier %q: %v", input, err)
		}
	}

	var peers []*p2p.PeerInfo
	if err := callRPC(ctx, &peers, "admin_peers"); err != nil {
		return "", fmt.Errorf("looking up node id for %s: %v", input, err)
	}

	wantIP := net.ParseIP(host)
	var matches []*p2p.PeerInfo
	for _, p := range peers {
		// p.Enode is the peer's authenticated enode URL with its
		// advertised listen address (not the ephemeral socket port
		// that RemoteAddress would report for inbound peers).
		u, err := url.Parse(p.Enode)
		if err != nil || u.Host == "" {
			continue
		}
		peerHost, peerPort, err := net.SplitHostPort(u.Host)
		if err != nil {
			continue
		}
		if port != "" && peerPort != port {
			continue
		}
		if wantIP != nil {
			if ip := net.ParseIP(peerHost); ip == nil || !ip.Equal(wantIP) {
				continue
			}
		} else if peerHost != host {
			continue
		}
		matches = append(matches, p)
	}

	switch len(matches) {
	case 0:
		return "", fmt.Errorf("no currently connected peer at %s — pass the full enode:// URL instead", input)
	case 1:
		return matches[0].Enode, nil
	default:
		// Ambiguous: multiple peers share this host (e.g. two nodes
		// behind the same NAT). Make the user disambiguate with a
		// port, and show them the candidates.
		candidates := make([]string, 0, len(matches))
		for _, p := range matches {
			candidates = append(candidates, p.Enode)
		}
		return "", fmt.Errorf("multiple peers match %s; disambiguate with host:port or a full enode URL. Candidates:\n  %s",
			input, strings.Join(candidates, "\n  "))
	}
}
