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

// This file contains a miner stress test based on the XHash consensus engine.
package main

import (
	"crypto/ecdsa"
	"math/big"
	"math/rand"
	"os"
	"os/signal"
	"time"

	"github.com/ParallaxProtocol/parallax"
	"github.com/ParallaxProtocol/parallax/crypto"
	"github.com/ParallaxProtocol/parallax/kernel/chainparams"
	"github.com/ParallaxProtocol/parallax/kernel/xhash"
	"github.com/ParallaxProtocol/parallax/logging"
	"github.com/ParallaxProtocol/parallax/node"
	protocol "github.com/ParallaxProtocol/parallax/node/fullnode"
	"github.com/ParallaxProtocol/parallax/node/fullnode/downloader"
	"github.com/ParallaxProtocol/parallax/node/miner"
	"github.com/ParallaxProtocol/parallax/node/nodeconfig"
	"github.com/ParallaxProtocol/parallax/p2p"
	"github.com/ParallaxProtocol/parallax/p2p/enode"
	"github.com/ParallaxProtocol/parallax/primitives/types"
	"github.com/ParallaxProtocol/parallax/util"
	"github.com/ParallaxProtocol/parallax/util/fdlimit"
	"github.com/ParallaxProtocol/parallax/validation"
)

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
		stacks = append(stacks, stack)
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
	nonces := make([]uint64, len(faucets))
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

		// Create a self transaction and inject into the pool
		tx, err := types.SignTx(types.NewTransaction(nonces[index], crypto.PubkeyToAddress(faucets[index].PublicKey), new(big.Int), 21000, big.NewInt(100000000000+rand.Int63n(65536)), nil), types.HomesteadSigner{}, faucets[index])
		if err != nil {
			panic(err)
		}
		if err := backend.TxPool().AddLocal(tx); err != nil {
			panic(err)
		}
		nonces[index]++

		// Wait if we're too saturated
		if pend, _ := backend.TxPool().Stats(); pend > 2048 {
			time.Sleep(100 * time.Millisecond)
		}
	}
}

// makeGenesis creates a custom XHash genesis block based on some pre-defined
// faucet accounts.
func makeGenesis(faucets []*ecdsa.PrivateKey) *validation.Genesis {
	genesis := validation.DefaultTestnetGenesisBlock()
	genesis.Difficulty = chainparams.MinimumDifficulty
	genesis.GasLimit = 25000000

	genesis.Config.ChainID = big.NewInt(18)
	genesis.Config.EIP150Hash = util.Hash{}

	genesis.Alloc = validation.GenesisAlloc{}
	for _, faucet := range faucets {
		genesis.Alloc[crypto.PubkeyToAddress(faucet.PublicKey)] = validation.GenesisAccount{
			Balance: new(big.Int).Exp(big.NewInt(2), big.NewInt(128), nil),
		}
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
