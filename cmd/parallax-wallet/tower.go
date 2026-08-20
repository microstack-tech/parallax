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
	"github.com/ParallaxProtocol/parallax/v2/util"
	"github.com/ParallaxProtocol/parallax/v2/wallet/channels/towerd"
	"gopkg.in/urfave/cli.v1"
)

var commandTower = cli.Command{
	Name:      "tower",
	Usage:     "run a watchtower (challenge stale closes for delegators)",
	ArgsUsage: "<keyfile>",
	Description: `
Runs the channel node in tower mode (Part 3 §10): accepts delegated states
over Nostr (kind 21906) from the npubs in [tower].delegators, watches every
CloseStarted on the configured registry, and challenges any close that is
stale against a held delegation. The keyfile's account pays challenge gas
and receives the on-chain refund. The tower can be lazy, never a thief:
delegated material only fits the anyone-can-call challenge.`,
	Flags:  []cli.Flag{passphraseFlag, channelConfigFlag, channelDataDirFlag},
	Action: towerDaemon,
}

func towerDaemon(ctx *cli.Context) error {
	node, cleanup, err := newChannelNode(ctx, true)
	if err != nil {
		return err
	}
	defer cleanup()

	cfg := node.Cfg
	if !cfg.Tower.Enabled {
		utils.Fatalf("set tower.enabled = true in the config to run a tower")
	}
	delegators := make(map[string]bool, len(cfg.Tower.Delegators))
	for _, npub := range cfg.Tower.Delegators {
		delegators[npub] = true
	}
	minDiscrepancy := new(big.Int)
	if s := cfg.Tower.MinDiscrepancyWei; s != "" {
		if _, ok := minDiscrepancy.SetString(s, 10); !ok {
			utils.Fatalf("bad tower.min_discrepancy_wei %q", s)
		}
	}

	// One tower instance per configured registry.
	var towers []*towerd.Tower
	for label, entries := range cfg.Registries {
		for _, e := range entries {
			t, err := towerd.New(towerd.Config{
				ChainID:               fmt.Sprintf("%d", e.ChainID),
				Registry:              util.HexToAddress(e.Address),
				Confirmations:         cfg.Node.Confirmations,
				Delegators:            delegators,
				OpenRegistration:      cfg.Tower.OpenRegistration,
				MaxDelegationsPerNpub: cfg.Tower.MaxDelegationsPerNpub,
				MinDiscrepancyWei:     minDiscrepancy,
			}, node.Store, node.Backend(), node.EVMKey())
			if err != nil {
				return fmt.Errorf("tower for registry %s: %w", label, err)
			}
			t.Alarm = func(format string, args ...any) {
				fmt.Fprintf(os.Stderr, "TOWER ALARM: "+format+"\n", args...)
			}
			towers = append(towers, t)
		}
	}
	if len(towers) == 1 {
		node.DelegationHandler = towers[0].HandleDelegation
	} else {
		// Route by registry when several coexist.
		node.DelegationHandler = towerd.Route(towers)
	}

	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sig
		cancel()
	}()

	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				for _, t := range towers {
					if _, err := t.Tick(runCtx); err != nil {
						fmt.Fprintf(os.Stderr, "tower tick: %v\n", err)
					}
				}
			case <-runCtx.Done():
				return
			}
		}
	}()

	fmt.Printf("tower running: address %s npub %s (%d delegators%s)\n",
		node.Signer.Address().Hex(), node.SelfPub, len(delegators),
		map[bool]string{true: ", open registration", false: ""}[cfg.Tower.OpenRegistration])
	node.Run(runCtx, 30*time.Second)
	return nil
}
