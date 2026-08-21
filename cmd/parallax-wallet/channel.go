// Copyright 2026 The Parallax Protocol Authors
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
	"fmt"
	"math/big"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ParallaxProtocol/parallax/v2/cmd/utils"
	"github.com/ParallaxProtocol/parallax/v2/rpc/client"
	"github.com/ParallaxProtocol/parallax/v2/util"
	"github.com/ParallaxProtocol/parallax/v2/wallet/channels/channeld"
	"github.com/ParallaxProtocol/parallax/v2/wallet/channels/nostrmod"
	"github.com/ParallaxProtocol/parallax/v2/wallet/channels/proofstore"
	"github.com/ParallaxProtocol/parallax/v2/wallet/keystore"
	"gopkg.in/urfave/cli.v1"
)

var (
	channelConfigFlag = cli.StringFlag{
		Name:  "config",
		Usage: "channel configuration file (TOML)",
	}
	channelDataDirFlag = cli.StringFlag{
		Name:  "channeldata",
		Usage: "directory holding the channel proof store (default: <config dir>)",
	}
	channelNpubFlag = cli.StringFlag{
		Name:  "npub",
		Usage: "counterparty Nostr public key (64-char x-only hex)",
	}
	channelDepositFlag = cli.StringFlag{
		Name:  "deposit",
		Usage: "initial deposit in wei",
	}
	channelPeriodFlag = cli.UintFlag{
		Name:  "period",
		Usage: "challenge period in blocks (default: config default_challenge_period)",
	}
	channelInvoiceFlag = cli.StringFlag{
		Name:  "invoice",
		Usage: "invoice id being paid",
	}
	channelUnilateralFlag = cli.BoolFlag{
		Name:  "unilateral",
		Usage: "force-close on-chain instead of negotiating cooperatively",
	}
	channelForceFlag = cli.BoolFlag{
		Name:  "force",
		Usage: "accept the displayed penalty exposure when closing a poisoned channel",
	}
	channelWaitFlag = cli.DurationFlag{
		Name:  "wait",
		Usage: "how long to wait for asynchronous completion",
		Value: 60 * time.Second,
	}
	channelTTLFlag = cli.DurationFlag{
		Name:  "ttl",
		Usage: "invoice validity",
		Value: 15 * time.Minute,
	}
	channelPinFlag = cli.Uint64Flag{
		Name:  "channel",
		Usage: "pin the invoice to a channel id (also sends it over the relay)",
	}
	channelMemoFlag = cli.StringFlag{
		Name:  "memo",
		Usage: "invoice memo",
	}
	channelURIFlag = cli.StringFlag{
		Name:  "uri",
		Usage: "pay a parallax: payment URI (amount and invoice come from it)",
	}
)

var commandChannel = cli.Command{
	Name:  "channel",
	Usage: "manage payment channels (Parallax Channels spec)",
	Subcommands: []cli.Command{
		{
			Name:      "open",
			Usage:     "open a channel on-chain and handshake the counterparty",
			ArgsUsage: "<keyfile> <counterparty-address>",
			Flags: []cli.Flag{
				passphraseFlag, channelConfigFlag, channelDataDirFlag,
				channelNpubFlag, channelDepositFlag, channelPeriodFlag, channelWaitFlag,
			},
			Action: channelOpen,
		},
		{
			Name:      "list",
			Usage:     "list channels with balances and flags",
			ArgsUsage: "<keyfile>",
			Flags:     []cli.Flag{passphraseFlag, channelConfigFlag, channelDataDirFlag, jsonFlag},
			Action:    channelList,
		},
		{
			Name:      "deposit",
			Usage:     "top up an open channel",
			ArgsUsage: "<keyfile> <channel-id> <amount-wei>",
			Flags:     []cli.Flag{passphraseFlag, channelConfigFlag, channelDataDirFlag},
			Action:    channelDeposit,
		},
		{
			Name:      "pay",
			Usage:     "pay over a channel and wait for the countersignature",
			ArgsUsage: "<keyfile> [<channel-id> <amount-wei>]",
			Flags: []cli.Flag{
				passphraseFlag, channelConfigFlag, channelDataDirFlag,
				channelInvoiceFlag, channelURIFlag, channelWaitFlag,
			},
			Action: channelPay,
		},
		{
			Name:      "invoice",
			Usage:     "create an invoice and print its payment URI",
			ArgsUsage: "<keyfile> <amount-wei>",
			Flags: []cli.Flag{
				passphraseFlag, channelConfigFlag, channelDataDirFlag,
				channelTTLFlag, channelPinFlag, channelMemoFlag, jsonFlag,
			},
			Action: channelInvoice,
		},
		{
			Name:      "withdraw",
			Usage:     "cooperatively withdraw from a channel while it stays open",
			ArgsUsage: "<keyfile> <channel-id> <amount-wei>",
			Flags: []cli.Flag{
				passphraseFlag, channelConfigFlag, channelDataDirFlag, channelWaitFlag,
			},
			Action: channelWithdraw,
		},
		{
			Name:      "close",
			Usage:     "close a channel (cooperative by default)",
			ArgsUsage: "<keyfile> <channel-id>",
			Flags: []cli.Flag{
				passphraseFlag, channelConfigFlag, channelDataDirFlag,
				channelUnilateralFlag, channelForceFlag, channelWaitFlag,
			},
			Action: channelClose,
		},
		commandChannelQR,
		{
			Name:      "daemon",
			Usage:     "run the wallet channel node (receive payments, watch the chain)",
			ArgsUsage: "<keyfile>",
			Flags:     []cli.Flag{passphraseFlag, channelConfigFlag, channelDataDirFlag},
			Action:    channelDaemon,
		},
	},
}

var commandNostr = cli.Command{
	Name:  "nostr",
	Usage: "channel transport identity operations",
	Subcommands: []cli.Command{
		{
			Name:      "whoami",
			Usage:     "print the derived Nostr identity for a keyfile",
			ArgsUsage: "<keyfile>",
			Flags:     []cli.Flag{passphraseFlag, channelConfigFlag, channelDataDirFlag, jsonFlag},
			Action:    nostrWhoami,
		},
		{
			Name:      "link-publish",
			Usage:     "publish the npub-to-address linkage (merchants)",
			ArgsUsage: "<keyfile>",
			Flags:     []cli.Flag{passphraseFlag, channelConfigFlag, channelDataDirFlag},
			Action:    nostrLinkPublish,
		},
		{
			Name:      "link-revoke",
			Usage:     "revoke a published linkage",
			ArgsUsage: "<keyfile>",
			Flags:     []cli.Flag{passphraseFlag, channelConfigFlag, channelDataDirFlag},
			Action:    nostrLinkRevoke,
		},
		{
			Name:      "backup-restore",
			Usage:     "restore channel state from relay self-backups after storage loss",
			ArgsUsage: "<keyfile>",
			Flags:     []cli.Flag{passphraseFlag, channelConfigFlag, channelDataDirFlag, channelWaitFlag},
			Action:    nostrBackupRestore,
		},
	},
}

// newChannelNode loads config + keyfile and assembles the channel node.
func newChannelNode(ctx *cli.Context, needChain bool) (*channeld.Node, func(), error) {
	keyfilepath := ctx.Args().First()
	if keyfilepath == "" {
		utils.Fatalf("keyfile argument required")
	}
	configPath := ctx.String(channelConfigFlag.Name)
	if configPath == "" {
		utils.Fatalf("--config required")
	}
	cfg, err := channeld.LoadConfig(configPath)
	if err != nil {
		return nil, nil, err
	}

	keyjson, err := os.ReadFile(keyfilepath)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to read keyfile %s: %w", keyfilepath, err)
	}
	passphrase := getPassphrase(ctx, false)
	key, err := keystore.DecryptKey(keyjson, passphrase)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to decrypt keyfile: %w", err)
	}

	dataDir := ctx.String(channelDataDirFlag.Name)
	if dataDir == "" {
		dataDir = "."
	}

	var backend channeld.Backend
	var closeBackend func()
	if needChain {
		rpcClient, err := client.Dial(cfg.Node.RPC)
		if err != nil {
			return nil, nil, fmt.Errorf("dialing node rpc %s: %w", cfg.Node.RPC, err)
		}
		backend = rpcClient
		closeBackend = rpcClient.Close
	}

	node, err := channeld.New(cfg, dataDir, key.PrivateKey, backend, nil)
	if err != nil {
		if closeBackend != nil {
			closeBackend()
		}
		return nil, nil, err
	}
	cleanup := func() {
		node.Close()
		if closeBackend != nil {
			closeBackend()
		}
	}
	return node, cleanup, nil
}

// runNodeFor runs the node's loops for the duration of fn.
func runNodeFor(node *channeld.Node, fn func(ctx context.Context) error) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go node.Run(ctx, 5*time.Second)
	return fn(ctx)
}

func argWei(ctx *cli.Context, index int) *big.Int {
	v, ok := new(big.Int).SetString(ctx.Args().Get(index), 10)
	if !ok || v.Sign() <= 0 {
		utils.Fatalf("bad wei amount %q", ctx.Args().Get(index))
	}
	return v
}

// argChannel resolves a channel argument: a bare decimal id, or the
// qualified <chainId>:<registry>:<id> form when coexisting registries share
// the id.
func argChannel(node *channeld.Node, ctx *cli.Context, index int) proofstore.ChannelKey {
	key, err := node.ParseChannelRef(ctx.Args().Get(index))
	if err != nil {
		utils.Fatalf("%v", err)
	}
	return key
}

func channelOpen(ctx *cli.Context) error {
	node, cleanup, err := newChannelNode(ctx, true)
	if err != nil {
		return err
	}
	defer cleanup()

	counterparty := ctx.Args().Get(1)
	if !util.IsHexAddress(counterparty) {
		utils.Fatalf("bad counterparty address %q", counterparty)
	}
	npub := ctx.String(channelNpubFlag.Name)
	if len(npub) != 64 {
		utils.Fatalf("--npub must be 64 hex chars (the counterparty's x-only key)")
	}
	deposit := new(big.Int)
	if d := ctx.String(channelDepositFlag.Name); d != "" {
		if _, ok := deposit.SetString(d, 10); !ok {
			utils.Fatalf("bad --deposit %q", d)
		}
	}

	// One configured registry entry expected for open (use the first).
	var entry channeld.RegistryEntry
	for _, entries := range node.Cfg.Registries {
		if len(entries) > 0 {
			entry = entries[0]
			break
		}
	}

	return runNodeFor(node, func(runCtx context.Context) error {
		key, err := node.OpenChannel(runCtx, entry.ChainID, util.HexToAddress(entry.Address),
			util.HexToAddress(counterparty), npub, deposit, uint32(ctx.Uint(channelPeriodFlag.Name)))
		if err != nil {
			return err
		}
		fmt.Printf("Channel %d open on registry %s\n", key.ChannelID, key.Registry.Hex())
		// Give the handshake a delivery window; the persistent queue keeps
		// retrying on the next run either way.
		time.Sleep(5 * time.Second)
		return nil
	})
}

func channelList(ctx *cli.Context) error {
	// Prefer a chain-connected node so balances reflect a fresh watcher
	// pass; fall back to the offline store view when the RPC is down.
	node, cleanup, err := newChannelNode(ctx, true)
	if err != nil {
		node, cleanup, err = newChannelNode(ctx, false)
	}
	if err != nil {
		return err
	}
	defer cleanup()
	for _, w := range node.Watchers {
		if _, err := w.Tick(context.Background()); err != nil {
			return err
		}
	}

	metas, err := node.Store.ListChannels()
	if err != nil {
		return err
	}
	type row struct {
		Channel   uint64 `json:"channelId"`
		Registry  string `json:"registry"`
		Role      string `json:"role"`
		Status    string `json:"status"`
		Peer      string `json:"peer"`
		Seq       uint64 `json:"seq"`
		BalanceA  string `json:"balanceA"`
		BalanceB  string `json:"balanceB"`
		Poisoned  bool   `json:"poisoned,omitempty"`
		FrozenTil uint64 `json:"frozenUntilBlock,omitempty"`
	}
	var rows []row
	for _, meta := range metas {
		r := row{
			Channel: meta.Key.ChannelID, Registry: meta.Key.Registry.Hex(),
			Role: string(meta.Role), Status: string(meta.Status),
			Peer: meta.PeerAddress.Hex(), Poisoned: meta.Poisoned, FrozenTil: meta.FrozenUntilBlock,
		}
		if latest, err := node.Store.LatestState(meta.Key); err == nil {
			r.Seq = latest.Seq
		}
		if balA, balB, err := node.Engine.CloseBalances(meta.Key); err == nil {
			r.BalanceA, r.BalanceB = balA.String(), balB.String()
		}
		rows = append(rows, r)
	}
	if ctx.Bool(jsonFlag.Name) {
		mustPrintJSON(rows)
		return nil
	}
	for _, r := range rows {
		fmt.Printf("channel %d  %s  role %s  status %s  seq %d  balA %s  balB %s  peer %s",
			r.Channel, r.Registry, r.Role, r.Status, r.Seq, r.BalanceA, r.BalanceB, r.Peer)
		if r.Poisoned {
			fmt.Printf("  POISONED")
		}
		if r.FrozenTil > 0 {
			fmt.Printf("  FROZEN<=%d", r.FrozenTil)
		}
		fmt.Println()
	}
	return nil
}

func channelDeposit(ctx *cli.Context) error {
	node, cleanup, err := newChannelNode(ctx, true)
	if err != nil {
		return err
	}
	defer cleanup()
	key := argChannel(node, ctx, 1)
	return node.Deposit(context.Background(), key, argWei(ctx, 2))
}

func channelPay(ctx *cli.Context) error {
	node, cleanup, err := newChannelNode(ctx, true)
	if err != nil {
		return err
	}
	defer cleanup()

	var key proofstore.ChannelKey
	var amount *big.Int
	invoiceID := ctx.String(channelInvoiceFlag.Name)
	if uri := ctx.String(channelURIFlag.Name); uri != "" {
		req, err := channeld.ParsePaymentURI(uri)
		if err != nil {
			return err
		}
		if key, err = node.ChannelForRequest(req); err != nil {
			return err
		}
		amount, invoiceID = req.AmountWei, req.InvoiceID
	} else {
		key = argChannel(node, ctx, 1)
		amount = argWei(ctx, 2)
	}

	before, _ := node.Store.LatestState(key)
	return runNodeFor(node, func(runCtx context.Context) error {
		if err := node.Pay(runCtx, key, amount, invoiceID); err != nil {
			return err
		}
		deadline := time.Now().Add(ctx.Duration(channelWaitFlag.Name))
		for time.Now().Before(deadline) {
			latest, err := node.Store.LatestState(key)
			if err == nil && latest.Seq > before.Seq {
				fmt.Printf("payment complete at seq %d\n", latest.Seq)
				return nil
			}
			time.Sleep(200 * time.Millisecond)
		}
		exposure, _ := node.Engine.PoisonedExposure(key)
		return fmt.Errorf("no countersignature yet; the proposal keeps retransmitting from the persistent queue. Channel exposure if closed now: %s wei", exposure)
	})
}

func channelInvoice(ctx *cli.Context) error {
	node, cleanup, err := newChannelNode(ctx, false)
	if err != nil {
		return err
	}
	defer cleanup()

	inv, uri, err := node.CreateInvoice(argWei(ctx, 1),
		ctx.String(channelMemoFlag.Name), ctx.Duration(channelTTLFlag.Name), ctx.Uint64(channelPinFlag.Name))
	if err != nil {
		return err
	}
	if ctx.Bool(jsonFlag.Name) {
		mustPrintJSON(map[string]any{
			"invoiceId": inv.ID, "amountWei": inv.AmountWei.BigInt().String(),
			"expiresAt": inv.ExpiresAt, "uri": uri,
		})
		return nil
	}
	fmt.Printf("invoice: %s\nexpires: %s\nuri:     %s\n", inv.ID, time.Unix(inv.ExpiresAt, 0), uri)
	if inv.ChannelID != 0 {
		fmt.Println("note: run the merchant daemon so the invoice reaches the payer and payments are countersigned")
	}
	return nil
}

func channelWithdraw(ctx *cli.Context) error {
	node, cleanup, err := newChannelNode(ctx, true)
	if err != nil {
		return err
	}
	defer cleanup()
	key := argChannel(node, ctx, 1)
	amount := argWei(ctx, 2)

	dep, err := node.Store.Deposits(key)
	if err != nil {
		return err
	}
	meta, err := node.Store.Meta(key)
	if err != nil {
		return err
	}
	before := dep.WithdrawnA.BigInt()
	if meta.Role == proofstore.RoleB {
		before = dep.WithdrawnB.BigInt()
	}
	want := new(big.Int).Add(before, amount)

	return runNodeFor(node, func(runCtx context.Context) error {
		if err := node.Withdraw(runCtx, key, amount); err != nil {
			return err
		}
		deadline := time.Now().Add(ctx.Duration(channelWaitFlag.Name))
		for time.Now().Before(deadline) {
			dep, err := node.Store.Deposits(key)
			if err == nil {
				got := dep.WithdrawnA.BigInt()
				if meta.Role == proofstore.RoleB {
					got = dep.WithdrawnB.BigInt()
				}
				if got.Cmp(want) >= 0 {
					fmt.Printf("withdrawal confirmed on-chain: cumulative %s wei\n", got)
					return nil
				}
			}
			time.Sleep(500 * time.Millisecond)
		}
		return fmt.Errorf("withdrawal not confirmed yet; the proposal keeps retransmitting until its expiry")
	})
}

func channelClose(ctx *cli.Context) error {
	node, cleanup, err := newChannelNode(ctx, true)
	if err != nil {
		return err
	}
	defer cleanup()
	key := argChannel(node, ctx, 1)

	if ctx.Bool(channelUnilateralFlag.Name) {
		return node.UnilateralClose(context.Background(), key, ctx.Bool(channelForceFlag.Name))
	}
	return runNodeFor(node, func(runCtx context.Context) error {
		if err := node.CoopClose(runCtx, key); err != nil {
			return err
		}
		deadline := time.Now().Add(ctx.Duration(channelWaitFlag.Name))
		for time.Now().Before(deadline) {
			meta, err := node.Store.Meta(key)
			if err == nil && meta.Status == proofstore.StatusSettled {
				fmt.Println("channel settled cooperatively")
				return nil
			}
			time.Sleep(500 * time.Millisecond)
		}
		return fmt.Errorf("cooperative close not settled yet; the channel stays frozen until the proposal expires, then resumes")
	})
}

func channelDaemon(ctx *cli.Context) error {
	node, cleanup, err := newChannelNode(ctx, true)
	if err != nil {
		return err
	}
	defer cleanup()

	fmt.Printf("channel node running: address %s npub %s\n", node.Signer.Address().Hex(), node.SelfPub)
	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sig
		cancel()
	}()
	node.Run(runCtx, 5*time.Second)
	return nil
}

func nostrWhoami(ctx *cli.Context) error {
	node, cleanup, err := newChannelNode(ctx, false)
	if err != nil {
		return err
	}
	defer cleanup()
	out := struct {
		Address string `json:"evmAddress"`
		Npub    string `json:"npubHex"`
	}{node.Signer.Address().Hex(), node.SelfPub}
	if ctx.Bool(jsonFlag.Name) {
		mustPrintJSON(out)
		return nil
	}
	fmt.Printf("address: %s\nnpub:    %s\n", out.Address, out.Npub)
	return nil
}

func nostrLinkPublish(ctx *cli.Context) error {
	return publishLinkage(ctx, false)
}

func nostrLinkRevoke(ctx *cli.Context) error {
	return publishLinkage(ctx, true)
}

func publishLinkage(ctx *cli.Context, revoke bool) error {
	node, cleanup, err := newChannelNode(ctx, false)
	if err != nil {
		return err
	}
	defer cleanup()

	ev, err := nostrmod.BuildLinkageEvent(node.Signer, node.NostrPriv)
	if revoke {
		ev, err = nostrmod.BuildLinkageRevocation(node.Signer, node.NostrPriv)
	}
	if err != nil {
		return err
	}
	return runNodeFor(node, func(runCtx context.Context) error {
		deadline := time.Now().Add(15 * time.Second)
		for time.Now().Before(deadline) {
			if n := node.Pool.Publish(runCtx, ev); n > 0 {
				fmt.Printf("linkage published to %d relays\n", n)
				return nil
			}
			time.Sleep(500 * time.Millisecond)
		}
		return fmt.Errorf("no relay accepted the linkage event")
	})
}

func nostrBackupRestore(ctx *cli.Context) error {
	node, cleanup, err := newChannelNode(ctx, true)
	if err != nil {
		return err
	}
	defer cleanup()

	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go node.Pool.Run(runCtx) // pool only: the dispatcher must not race the collector

	n, err := node.RestoreFromBackups(runCtx, ctx.Duration(channelWaitFlag.Name))
	if err != nil {
		return err
	}
	fmt.Printf("restored %d channels; reconciling against the chain before use\n", n)
	// The mandatory post-restore reconcile (Part 3 §4.3).
	for _, w := range node.Watchers {
		if _, err := w.Tick(runCtx); err != nil {
			return fmt.Errorf("post-restore reconcile: %w", err)
		}
	}
	return nil
}
