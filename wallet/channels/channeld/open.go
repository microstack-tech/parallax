package channeld

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"time"

	"github.com/ParallaxProtocol/parallax/v2/primitives/types"
	"github.com/ParallaxProtocol/parallax/v2/script/abi/bind"
	"github.com/ParallaxProtocol/parallax/v2/util"
	"github.com/ParallaxProtocol/parallax/v2/wallet/channels/nostrmod"
	"github.com/ParallaxProtocol/parallax/v2/wallet/channels/proofstore"
	"github.com/ParallaxProtocol/parallax/v2/wallet/channels/protocol"
	"github.com/ParallaxProtocol/parallax/v2/wallet/channels/registry"
)

// contractFor returns a binding for one configured registry deployment.
func (n *Node) contractFor(chainID string, regAddr util.Address) (*registry.ChannelRegistry, error) {
	if n.backend == nil {
		return nil, errors.New("channeld: no chain backend configured")
	}
	for _, entries := range n.Cfg.Registries {
		for _, e := range entries {
			if strconv.FormatUint(e.ChainID, 10) == chainID && util.HexToAddress(e.Address) == regAddr {
				return registry.NewChannelRegistry(regAddr, n.backend)
			}
		}
	}
	return nil, fmt.Errorf("channeld: registry %s (chain %s) not configured", regAddr.Hex(), chainID)
}

// OpenChannel opens a channel on-chain as participant A, records it locally,
// and queues the 21908 handshake carrying the private linkage (Part 2 §6.8).
// The counterparty's npub comes from its published linkage or a payment URI.
func (n *Node) OpenChannel(ctx context.Context, chainID uint64, regAddr, counterparty util.Address, counterpartyNpub string, deposit *big.Int, period uint32) (proofstore.ChannelKey, error) {
	var key proofstore.ChannelKey
	if period == 0 {
		period = n.Cfg.Channels.DefaultChallengePeriod
	}
	chain := strconv.FormatUint(chainID, 10)
	contract, err := n.contractFor(chain, regAddr)
	if err != nil {
		return key, err
	}

	auth, err := bind.NewKeyedTransactorWithChainID(n.evmKey, new(big.Int).SetUint64(chainID))
	if err != nil {
		return key, err
	}
	auth.Context = ctx
	auth.Value = deposit
	tx, err := contract.Open(auth, counterparty, period)
	if err != nil {
		return key, fmt.Errorf("channeld: open: %w", err)
	}
	receipt, err := bind.WaitMined(ctx, n.backend, tx)
	if err != nil {
		return key, err
	}

	var channelID uint64
	filterer := &contract.ChannelRegistryFilterer
	for _, log := range receipt.Logs {
		if ev, perr := filterer.ParseChannelOpened(*log); perr == nil {
			channelID = ev.ChannelId.Uint64()
			break
		}
	}
	if channelID == 0 {
		return key, errors.New("channeld: ChannelOpened event not found in receipt")
	}
	key = proofstore.ChannelKey{ChainID: chain, Registry: regAddr, ChannelID: channelID}

	err = n.Store.CreateChannel(proofstore.ChannelMeta{
		Key:             key,
		Role:            proofstore.RoleA,
		Status:          proofstore.StatusOpen,
		PeerNpub:        counterpartyNpub,
		PeerAddress:     counterparty,
		ChallengePeriod: period,
		OpenedAtBlock:   receipt.BlockNumber.Uint64(),
	})
	if err != nil {
		return key, err
	}
	return key, n.sendHandshake(key, counterpartyNpub)
}

// sendHandshake queues the 21908 with the private linkage over our npub.
// Handshakes have no ACK; duplicates are idempotent on the receiving side,
// and retransmission stops at expiry.
func (n *Node) sendHandshake(key proofstore.ChannelKey, peerNpub string) error {
	evmSig, err := nostrmod.SignLinkage(n.Signer, n.SelfPub)
	if err != nil {
		return err
	}
	msg := protocol.HandshakeMsg{
		V:          1,
		ChannelID:  strconv.FormatUint(key.ChannelID, 10),
		Registry:   key.Registry.Hex(),
		ChainID:    key.ChainID,
		EVMAddress: n.Signer.Address().Hex(),
		Relays:     n.Cfg.Nostr.Relays,
	}
	msg.Linkage.EVMSig = evmSig
	content, err := protocol.EncodePayload(msg)
	if err != nil {
		return err
	}
	now := time.Now().Unix()
	_, err = n.Store.EnqueueOutbound(proofstore.OutboundItem{
		DedupeKey: "21908:" + key.String(),
		ToNpub:    peerNpub,
		Kind:      protocol.KindHandshake,
		Content:   content,
		Tags:      [][]string{{"ch", strconv.FormatUint(key.ChannelID, 10)}},
		RumorTime: now,
		ExpiresAt: now + 6*3600,
	})
	return err
}

// handleHandshake runs the consent checks (Part 1 §7.1, Part 3 §8) before
// the channel becomes usable: on-chain parameters within policy, we really
// are participant B, and the sender's npub is bound to participant A's
// address by the private linkage. Only then is the channel recorded — and
// the first countersignature that follows is the consent the contract
// relies on.
func (n *Node) handleHandshake(ctx context.Context, msg protocol.HandshakeMsg, sender string) error {
	channelID, err := strconv.ParseUint(msg.ChannelID, 10, 64)
	if err != nil {
		return fmt.Errorf("channeld: bad handshake channelId %q", msg.ChannelID)
	}
	if !util.IsHexAddress(msg.Registry) || !util.IsHexAddress(msg.EVMAddress) {
		return errors.New("channeld: bad handshake addresses")
	}
	regAddr := util.HexToAddress(msg.Registry)
	opener := util.HexToAddress(msg.EVMAddress)

	contract, err := n.contractFor(msg.ChainID, regAddr)
	if err != nil {
		return err
	}
	onchain, err := contract.GetChannel(&bind.CallOpts{Context: ctx}, new(big.Int).SetUint64(channelID))
	if err != nil {
		return err
	}
	if onchain.State != 1 { // Open
		return fmt.Errorf("channeld: handshake channel %d not open on-chain", channelID)
	}
	if onchain.ParticipantA != opener || onchain.ParticipantB != n.Signer.Address() {
		return errors.New("channeld: handshake participants do not match on-chain channel")
	}
	if onchain.ChallengePeriodBlocks < n.Cfg.Channels.AcceptChallengePeriodMin ||
		onchain.ChallengePeriodBlocks > n.Cfg.Channels.AcceptChallengePeriodMax {
		return fmt.Errorf("channeld: challenge period %d outside policy [%d, %d]",
			onchain.ChallengePeriodBlocks, n.Cfg.Channels.AcceptChallengePeriodMin, n.Cfg.Channels.AcceptChallengePeriodMax)
	}
	if err := nostrmod.VerifyLinkage(sender, msg.Linkage.EVMSig, opener); err != nil {
		return fmt.Errorf("channeld: handshake linkage: %w", err)
	}

	err = n.Store.CreateChannel(proofstore.ChannelMeta{
		Key:             proofstore.ChannelKey{ChainID: msg.ChainID, Registry: regAddr, ChannelID: channelID},
		Role:            proofstore.RoleB,
		Status:          proofstore.StatusOpen,
		PeerNpub:        sender,
		PeerAddress:     opener,
		ChallengePeriod: onchain.ChallengePeriodBlocks,
		OpenedAtBlock:   onchain.OpenedAtBlock.Uint64(),
	})
	if errors.Is(err, proofstore.ErrExists) {
		return nil // duplicate handshake: idempotent
	}
	if err == nil {
		n.log.Info("channel handshake accepted", "channel", channelID, "peer", opener.Hex())
	}
	return err
}

// Deposit tops up this wallet's column on an open channel.
func (n *Node) Deposit(ctx context.Context, key proofstore.ChannelKey, amount *big.Int) error {
	contract, err := n.contractFor(key.ChainID, key.Registry)
	if err != nil {
		return err
	}
	chainID, _ := new(big.Int).SetString(key.ChainID, 10)
	auth, err := bind.NewKeyedTransactorWithChainID(n.evmKey, chainID)
	if err != nil {
		return err
	}
	auth.Context = ctx
	auth.Value = amount
	tx, err := contract.Deposit(auth, new(big.Int).SetUint64(key.ChannelID))
	if err != nil {
		return err
	}
	_, err = bind.WaitMined(ctx, n.backend, tx)
	return err
}

// UnilateralClose starts the on-chain dispute at the latest complete state
// (or with no proof when none exists). Refused by local policy while
// self-signed states are outstanding — closing below a seq the counterparty
// may hold walks into the penalty (Part 2 §7.4); pass force to accept the
// displayed exposure deliberately.
func (n *Node) UnilateralClose(ctx context.Context, key proofstore.ChannelKey, force bool) error {
	journal, err := n.Store.SelfSigned(key)
	if err != nil {
		return err
	}
	if len(journal) > 0 && !force {
		exposure, _ := n.Engine.PoisonedExposure(key)
		return fmt.Errorf("channeld: channel is poisoned; closing now risks a penalty of %s wei — use force to accept", exposure)
	}

	contract, err := n.contractFor(key.ChainID, key.Registry)
	if err != nil {
		return err
	}
	chainID, _ := new(big.Int).SetString(key.ChainID, 10)
	auth, err := bind.NewKeyedTransactorWithChainID(n.evmKey, chainID)
	if err != nil {
		return err
	}
	auth.Context = ctx

	latest, lerr := n.Store.LatestState(key)
	var tx *types.Transaction
	if lerr != nil {
		tx, err = contract.StartCloseNoProof(auth, new(big.Int).SetUint64(key.ChannelID))
	} else {
		tx, err = contract.StartClose(auth,
			new(big.Int).SetUint64(key.ChannelID),
			registry.ParallaxChannelRegistryBalanceProof{
				ChannelId:       new(big.Int).SetUint64(key.ChannelID),
				Seq:             latest.Seq,
				TransferredAtoB: latest.TransferredAtoB.BigInt(),
				TransferredBtoA: latest.TransferredBtoA.BigInt(),
				LocksRoot:       latest.LocksRoot,
				LockedAmount:    latest.LockedAmount.BigInt(),
			},
			latest.SigA, latest.SigB)
	}
	if err != nil {
		return err
	}
	_, err = bind.WaitMined(ctx, n.backend, tx)
	return err
}

// SubmitWithdraw sends the fully signed cooperative withdraw on-chain.
func (n *Node) SubmitWithdraw(ctx context.Context, ready *protocol.WithdrawReady) error {
	contract, err := n.contractFor(ready.Key.ChainID, ready.Key.Registry)
	if err != nil {
		return err
	}
	chainID, _ := new(big.Int).SetString(ready.Key.ChainID, 10)
	auth, err := bind.NewKeyedTransactorWithChainID(n.evmKey, chainID)
	if err != nil {
		return err
	}
	auth.Context = ctx
	tx, err := contract.CooperativeWithdraw(auth,
		new(big.Int).SetUint64(ready.Key.ChannelID),
		ready.Participant, ready.TotalWithdrawn,
		ready.ExpiryBlock, ready.SigA, ready.SigB)
	if err != nil {
		return err
	}
	_, err = bind.WaitMined(ctx, n.backend, tx)
	return err
}

// SubmitCoopClose sends the fully signed cooperative close on-chain.
func (n *Node) SubmitCoopClose(ctx context.Context, ready *protocol.CoopCloseReady) error {
	contract, err := n.contractFor(ready.Key.ChainID, ready.Key.Registry)
	if err != nil {
		return err
	}
	chainID, _ := new(big.Int).SetString(ready.Key.ChainID, 10)
	auth, err := bind.NewKeyedTransactorWithChainID(n.evmKey, chainID)
	if err != nil {
		return err
	}
	auth.Context = ctx
	tx, err := contract.CooperativeClose(auth,
		new(big.Int).SetUint64(ready.Key.ChannelID),
		ready.BalanceA, ready.BalanceB,
		ready.ExpiryBlock, ready.SigA, ready.SigB)
	if err != nil {
		return err
	}
	_, err = bind.WaitMined(ctx, n.backend, tx)
	return err
}
