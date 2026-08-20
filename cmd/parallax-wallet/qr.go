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
	"bufio"
	"fmt"
	"os"
	"strings"
	"time"

	qrcode "github.com/skip2/go-qrcode"
	"gopkg.in/urfave/cli.v1"
)

var commandChannelQR = cli.Command{
	Name:  "qr",
	Usage: "offline point-of-sale payments over QR codes (no network needed)",
	Subcommands: []cli.Command{
		{
			Name:      "show-invoice",
			Usage:     "create an invoice and display it as a QR code (merchant, step 1)",
			ArgsUsage: "<keyfile> <amount-wei>",
			Flags: []cli.Flag{
				passphraseFlag, channelConfigFlag, channelDataDirFlag,
				channelPinFlag, channelTTLFlag, channelMemoFlag,
			},
			Action: qrShowInvoice,
		},
		{
			Name:      "scan",
			Usage:     "process a scanned PLXC1 code from stdin (payer steps 2/4, merchant step 3)",
			ArgsUsage: "<keyfile>",
			Description: `
Reads one PLXC1:... payload from stdin and advances the offline exchange:
an invoice produces the payment proposal to show the merchant; a proposal
countersigns and produces the receipt to show the payer; a receipt completes
the payment. Pipe a camera scanner, e.g.:

    zbarcam --raw -1 | parallax-wallet channel qr scan --config ... key.json`,
			Flags:  []cli.Flag{passphraseFlag, channelConfigFlag, channelDataDirFlag},
			Action: qrScan,
		},
	},
}

func qrShowInvoice(ctx *cli.Context) error {
	node, cleanup, err := newChannelNode(ctx, false)
	if err != nil {
		return err
	}
	defer cleanup()

	channelID := ctx.Uint64(channelPinFlag.Name)
	if channelID == 0 {
		return fmt.Errorf("--channel is required: an offline payer cannot pick a channel")
	}
	inv, _, err := node.CreateInvoice(argWei(ctx, 1),
		ctx.String(channelMemoFlag.Name), ctx.Duration(channelTTLFlag.Name), channelID)
	if err != nil {
		return err
	}
	code, err := node.QRInvoice(inv)
	if err != nil {
		return err
	}
	fmt.Printf("invoice %s for %s wei (expires %s)\n\n", inv.ID, inv.AmountWei.BigInt(), time.Unix(inv.ExpiresAt, 0))
	printQR(code)
	return nil
}

func qrScan(ctx *cli.Context) error {
	node, cleanup, err := newChannelNode(ctx, false)
	if err != nil {
		return err
	}
	defer cleanup()

	fmt.Fprintln(os.Stderr, "paste the PLXC1 code (or pipe it) and press enter:")
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 0, 64*1024), 64*1024)
	var code string
	for scanner.Scan() {
		if line := strings.TrimSpace(scanner.Text()); line != "" {
			code = line
			break
		}
	}
	if code == "" {
		return fmt.Errorf("no code on stdin")
	}

	res, err := node.ScanQR(code)
	if err != nil {
		return err
	}
	fmt.Println(res.Summary)
	if res.Next != "" {
		fmt.Println()
		printQR(res.Next)
	}
	return nil
}

// printQR renders the code as ANSI half-blocks (two matrix rows per text
// line) plus the raw string for copy/paste when no camera is around.
func printQR(content string) {
	q, err := qrcode.New(content, qrcode.Medium)
	if err != nil {
		fmt.Println(content)
		return
	}
	bitmap := q.Bitmap()
	var b strings.Builder
	for y := 0; y < len(bitmap); y += 2 {
		for x := range bitmap[y] {
			top := bitmap[y][x]
			bottom := y+1 < len(bitmap) && bitmap[y+1][x]
			switch {
			case top && bottom:
				b.WriteRune('█')
			case top:
				b.WriteRune('▀')
			case bottom:
				b.WriteRune('▄')
			default:
				b.WriteRune(' ')
			}
		}
		b.WriteByte('\n')
	}
	fmt.Print(b.String())
	fmt.Printf("\n%s\n", content)
}
