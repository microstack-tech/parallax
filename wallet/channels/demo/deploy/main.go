// deploy is the demo helper that puts a ParallaxChannelRegistry on a devnet:
//
//	go run ./wallet/channels/demo/deploy --rpc http://127.0.0.1:8545 \
//	    --keyfile alice.json --password pw.txt [--refund 10000000000000000]
//
// Prints the deployed registry address on stdout.
package main

import (
	"context"
	"flag"
	"fmt"
	"math/big"
	"os"
	"strings"
	"time"

	"github.com/ParallaxProtocol/parallax/v2/rpc/client"
	"github.com/ParallaxProtocol/parallax/v2/script/abi/bind"
	"github.com/ParallaxProtocol/parallax/v2/wallet/channels/registry"
	"github.com/ParallaxProtocol/parallax/v2/wallet/keystore"
)

func main() {
	rpcURL := flag.String("rpc", "http://127.0.0.1:8545", "node RPC endpoint")
	keyfile := flag.String("keyfile", "", "deployer keyfile (json)")
	passfile := flag.String("password", "", "file holding the keyfile password")
	refund := flag.String("refund", "10000000000000000", "CHALLENGE_REFUND in wei (default 0.01 LAX)")
	flag.Parse()

	if err := run(*rpcURL, *keyfile, *passfile, *refund); err != nil {
		fmt.Fprintln(os.Stderr, "deploy:", err)
		os.Exit(1)
	}
}

func run(rpcURL, keyfile, passfile, refundStr string) error {
	keyjson, err := os.ReadFile(keyfile)
	if err != nil {
		return err
	}
	pass, err := os.ReadFile(passfile)
	if err != nil {
		return err
	}
	key, err := keystore.DecryptKey(keyjson, strings.TrimSpace(string(pass)))
	if err != nil {
		return err
	}
	refundWei, ok := new(big.Int).SetString(refundStr, 10)
	if !ok {
		return fmt.Errorf("bad refund %q", refundStr)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	ec, err := client.DialContext(ctx, rpcURL)
	if err != nil {
		return err
	}
	defer ec.Close()
	chainID, err := ec.ChainID(ctx)
	if err != nil {
		return err
	}

	auth, err := bind.NewKeyedTransactorWithChainID(key.PrivateKey, chainID)
	if err != nil {
		return err
	}
	auth.Context = ctx
	addr, tx, _, err := registry.DeployChannelRegistry(auth, ec, refundWei)
	if err != nil {
		return err
	}
	if _, err := bind.WaitMined(ctx, ec, tx); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "registry deployed on chain %s (tx %s)\n", chainID, tx.Hash().Hex())
	fmt.Println(addr.Hex())
	return nil
}
