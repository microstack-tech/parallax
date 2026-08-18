// Copyright 2025-2026 The Parallax Protocol Authors
// This file is part of the parallax library.
//
// The parallax library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The parallax library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the parallax library. If not, see <http://www.gnu.org/licenses/>.

// This file contains a miner stress test for eip 1559.
package main

import (
	"crypto/ecdsa"
	"math/big"
	"math/rand"
	"os"
	"os/signal"
	"time"

	parallax "github.com/ParallaxProtocol/parallax/v2"
	"github.com/ParallaxProtocol/parallax/v2/crypto"
	"github.com/ParallaxProtocol/parallax/v2/kernel/chainparams"
	"github.com/ParallaxProtocol/parallax/v2/kernel/xhash"
	"github.com/ParallaxProtocol/parallax/v2/logging"
	"github.com/ParallaxProtocol/parallax/v2/node"
	protocol "github.com/ParallaxProtocol/parallax/v2/node/fullnode"
	"github.com/ParallaxProtocol/parallax/v2/node/fullnode/downloader"
	"github.com/ParallaxProtocol/parallax/v2/node/miner"
	"github.com/ParallaxProtocol/parallax/v2/node/nodeconfig"
	"github.com/ParallaxProtocol/parallax/v2/p2p"
	"github.com/ParallaxProtocol/parallax/v2/p2p/enode"
	"github.com/ParallaxProtocol/parallax/v2/primitives/types"
	"github.com/ParallaxProtocol/parallax/v2/util"
	"github.com/ParallaxProtocol/parallax/v2/util/fdlimit"
	"github.com/ParallaxProtocol/parallax/v2/validation"
)

var londonBlock = big.NewInt(30) // Predefined london fork block for activating eip 1559.

func main() {
	logging.Root().SetHandler(logging.LvlFilterHandler(logging.LvlInfo, logging.StreamHandler(os.Stderr, logging.TerminalFormat(true))))
	fdlimit.Raise(2048)

	// Generate a batch of accounts to seal and fund with
	faucets := make([]*ecdsa.PrivateKey, 128)
	for i := 0; i < len(faucets); i++ {
		faucets[i], _ = crypto.GenerateKey()
	}
	// Pre-generate the XHash mining DAG so we don't race
	xhash.MakeDataset(1, nodeconfig.Defaults.XHash.DatasetDir)

	// Create an XHash network based off of the testnet config
	genesis := makeGenesis(faucets)

	// Handle interrupts.
	interruptCh := make(chan os.Signal, 5)
	signal.Notify(interruptCh, os.Interrupt)

	var (
		stacks []*node.Node
		nodes  []*protocol.Parallax
		enodes []*enode.Node
	)
	for i := 0; i < 4; i++ {
		// Start the node and wait until it's up
		stack, parallaxBackend, err := makeMiner(genesis)
		if err != nil {
			panic(err)
		}
		defer stack.Close()

		for stack.Server().NodeInfo().Ports.Listener == 0 {
			time.Sleep(250 * time.Millisecond)
		}
		// Connect the node to all the previous ones
		for _, n := range enodes {
			stack.Server().AddPeer(n)
		}
		// Start tracking the node and its enode
		nodes = append(nodes, parallaxBackend)
		enodes = append(enodes, stack.Server().Self())
	}

	// Iterate over all the nodes and start mining
	time.Sleep(3 * time.Second)
	for _, node := range nodes {
		if err := node.StartMining(1); err != nil {
			panic(err)
		}
	}
	time.Sleep(3 * time.Second)

	// Start injecting transactions from the faucets like crazy
	var (
		nonces = make([]uint64, len(faucets))

		// The signer activates the 1559 features even before the fork,
		// so the new 1559 txs can be created with this signer.
		signer = types.LatestSignerForChainID(genesis.Config.ChainID)
	)
	for {
		// Stop when interrupted.
		select {
		case <-interruptCh:
			for _, node := range stacks {
				node.Close()
			}
			return
		default:
		}

		// Pick a random mining node
		index := rand.Intn(len(faucets))
		backend := nodes[index%len(nodes)]

		headHeader := backend.BlockChain().CurrentHeader()
		baseFee := headHeader.BaseFee

		// Create a self transaction and inject into the pool. The legacy
		// and 1559 transactions can all be created by random even if the
		// fork is not happened.
		tx := makeTransaction(nonces[index], faucets[index], signer, baseFee)
		if err := backend.TxPool().AddLocal(tx); err != nil {
			continue
		}
		nonces[index]++

		// Wait if we're too saturated
		if pend, _ := backend.TxPool().Stats(); pend > 4192 {
			time.Sleep(100 * time.Millisecond)
		}

		// Wait if the basefee is raised too fast
		if baseFee != nil && baseFee.Cmp(new(big.Int).Mul(big.NewInt(100), big.NewInt(chainparams.GWei))) > 0 {
			time.Sleep(500 * time.Millisecond)
		}
	}
}

func makeTransaction(nonce uint64, privKey *ecdsa.PrivateKey, signer types.Signer, baseFee *big.Int) *types.Transaction {
	// Generate legacy transaction
	if rand.Intn(2) == 0 {
		tx, err := types.SignTx(types.NewTransaction(nonce, crypto.PubkeyToAddress(privKey.PublicKey), new(big.Int), 21000, big.NewInt(100000000000+rand.Int63n(65536)), nil), signer, privKey)
		if err != nil {
			panic(err)
		}
		return tx
	}
	// Generate eip 1559 transaction
	recipient := crypto.PubkeyToAddress(privKey.PublicKey)

	// Feecap and feetip are limited to 32 bytes. Offer a sightly
	// larger buffer for creating both valid and invalid transactions.
	buf := make([]byte, 32+5)
	rand.Read(buf)
	gasTipCap := new(big.Int).SetBytes(buf)

	// If the given base fee is nil(the 1559 is still not available),
	// generate a fake base fee in order to create 1559 tx forcibly.
	if baseFee == nil {
		baseFee = new(big.Int).SetInt64(int64(rand.Int31()))
	}
	// Generate the feecap, 75% valid feecap and 25% unguaranted.
	var gasFeeCap *big.Int
	if rand.Intn(4) == 0 {
		rand.Read(buf)
		gasFeeCap = new(big.Int).SetBytes(buf)
	} else {
		gasFeeCap = new(big.Int).Add(baseFee, gasTipCap)
	}
	return types.MustSignNewTx(privKey, signer, &types.DynamicFeeTx{
		ChainID:    signer.ChainID(),
		Nonce:      nonce,
		GasTipCap:  gasTipCap,
		GasFeeCap:  gasFeeCap,
		Gas:        21000,
		To:         &recipient,
		Value:      big.NewInt(100),
		Data:       nil,
		AccessList: nil,
	})
}

// makeGenesis creates a custom XHash genesis block based on some pre-defined
// faucet accounts.
func makeGenesis(faucets []*ecdsa.PrivateKey) *validation.Genesis {
	genesis := validation.DefaultTestnetGenesisBlock()

	genesis.Config = chainparams.AllXHashProtocolChanges
	genesis.Config.LondonBlock = londonBlock
	genesis.Difficulty = chainparams.MinimumDifficulty

	// Small gaslimit for easier basefee moving testing.
	genesis.GasLimit = 8_000_000

	genesis.Config.ChainID = big.NewInt(18)
	genesis.Config.EIP150Hash = util.Hash{}

	genesis.Alloc = validation.GenesisAlloc{}
	for _, faucet := range faucets {
		genesis.Alloc[crypto.PubkeyToAddress(faucet.PublicKey)] = validation.GenesisAccount{
			Balance: new(big.Int).Exp(big.NewInt(2), big.NewInt(128), nil),
		}
	}
	if londonBlock.Sign() == 0 {
		logging.Info("Enabled the eip 1559 by default")
	} else {
		logging.Info("Registered the london fork", "number", londonBlock)
	}
	return genesis
}

func makeMiner(genesis *validation.Genesis) (*node.Node, *protocol.Parallax, error) {
	// Define the basic configurations for the Parallax node
	datadir, _ := os.MkdirTemp("", "")

	config := &node.Config{
		Name:    "parallax",
		Version: parallax.Version,
		DataDir: datadir,
		P2P: p2p.Config{
			ListenAddr:  "0.0.0.0:0",
			NoDiscovery: true,
			MaxPeers:    25,
		},
		UseLightweightKDF: true,
	}
	// Create the node and configure a full Parallax node on it
	stack, err := node.New(config)
	if err != nil {
		return nil, nil, err
	}
	parallaxBackend, err := protocol.New(stack, &nodeconfig.Config{
		Genesis:         genesis,
		NetworkId:       genesis.Config.ChainID.Uint64(),
		SyncMode:        downloader.FullSync,
		DatabaseCache:   256,
		DatabaseHandles: 256,
		TxPool:          validation.DefaultTxPoolConfig,
		GPO:             nodeconfig.Defaults.GPO,
		XHash:           nodeconfig.Defaults.XHash,
		Miner: miner.Config{
			Coinbase: util.Address{1},
			GasCeil:  genesis.GasLimit * 11 / 10,
			GasPrice: big.NewInt(1),
			Recommit: time.Second,
		},
	})
	if err != nil {
		return nil, nil, err
	}
	err = stack.Start()
	return stack, parallaxBackend, err
}
