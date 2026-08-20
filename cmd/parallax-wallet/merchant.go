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
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/ParallaxProtocol/parallax/v2/cmd/utils"
	"github.com/ParallaxProtocol/parallax/v2/wallet/channels/merchantd"
	"gopkg.in/urfave/cli.v1"
)

var commandMerchant = cli.Command{
	Name:      "merchant",
	Usage:     "run the headless merchant daemon (REST API + webhooks)",
	ArgsUsage: "<keyfile>",
	Description: `
Runs the channel node with the merchant REST API (Part 3 §9): invoice
creation, invoice status, channel listing and closing, health and metrics.
The API listens on [merchant].listen from the config (default
127.0.0.1:9735) and requires the bearer token stored at
[merchant].auth_token_file.`,
	Flags:  []cli.Flag{passphraseFlag, channelConfigFlag, channelDataDirFlag},
	Action: merchantDaemon,
}

func merchantDaemon(ctx *cli.Context) error {
	node, cleanup, err := newChannelNode(ctx, true)
	if err != nil {
		return err
	}
	defer cleanup()

	listen := node.Cfg.Merchant.Listen
	if listen == "" {
		listen = "127.0.0.1:9735"
	}
	tokenFile := node.Cfg.Merchant.AuthTokenFile
	if tokenFile == "" {
		utils.Fatalf("merchant.auth_token_file must be set in the config")
	}
	raw, err := os.ReadFile(tokenFile)
	if err != nil {
		return fmt.Errorf("reading auth token: %w", err)
	}
	token := strings.TrimSpace(string(raw))
	if token == "" {
		utils.Fatalf("auth token file %s is empty", tokenFile)
	}

	server := merchantd.New(node, token, nil)
	httpServer := &http.Server{Addr: listen, Handler: server.Handler()}

	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sig
		cancel()
		shutdownCtx, done := context.WithTimeout(context.Background(), 3*time.Second)
		defer done()
		_ = httpServer.Shutdown(shutdownCtx)
	}()

	go server.RunWebhookWorker(runCtx, 5*time.Second)
	go func() {
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			utils.Fatalf("merchant api: %v", err)
		}
	}()

	fmt.Printf("merchant daemon: api http://%s  address %s  npub %s\n",
		listen, node.Signer.Address().Hex(), node.SelfPub)
	node.Run(runCtx, 5*time.Second)
	return nil
}
