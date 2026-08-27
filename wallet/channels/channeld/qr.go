package channeld

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"time"

	"github.com/ParallaxProtocol/parallax/v2/util"
	"github.com/ParallaxProtocol/parallax/v2/wallet/channels/proofstore"
	"github.com/ParallaxProtocol/parallax/v2/wallet/channels/protocol"
	"github.com/ParallaxProtocol/parallax/v2/wallet/channels/qrenc"
	"github.com/ParallaxProtocol/parallax/v2/wallet/channels/registry"
)

// The QR path (Part 2 §11) carries the same EIP-712-signed objects as the
// relay path with zero network on either side. Transport authentication
// (the seal npub check) does not exist here; the signatures themselves are
// the authentication, and the engine verifies them exactly as it does for
// relay traffic. The channel's stored peer npub is passed as the sender so
// the engine's transport check is satisfied by construction.

// QRResult is the outcome of scanning one code: the next code to display
// (empty when the exchange is finished on this side) plus a human summary.
type QRResult struct {
	Next    string // "PLXC1:…" to display, or ""
	Summary string
	// Complete is set when a payment finished on this side (payer: ACK
	// verified; merchant: countersigned).
	Complete *proofstore.SignedState
}

// keyFor resolves the envelope's channel against the store.
func (n *Node) keyFor(env qrenc.Envelope) (proofstore.ChannelKey, proofstore.ChannelMeta, error) {
	key := proofstore.ChannelKey{
		ChainID:   strconv.FormatUint(env.ChainID, 10),
		Registry:  env.Registry,
		ChannelID: env.ChannelID,
	}
	meta, err := n.Store.Meta(key)
	if err != nil {
		return key, meta, fmt.Errorf("channeld: unknown channel %d on registry %s", env.ChannelID, env.Registry.Hex())
	}
	return key, meta, nil
}

// bestKnownBlock is the freshest chain view an offline node has: the
// confirmed-scan watermark (Part 2 §11.2 step 3 — validation runs against
// last-known confirmed figures).
func (n *Node) bestKnownBlock(key proofstore.ChannelKey) uint64 {
	dep, err := n.Store.Deposits(key)
	if err != nil {
		return 0
	}
	return dep.LastScannedBlock
}

// QRInvoice renders an invoice as the type-1 code (merchant, step 1).
func (n *Node) QRInvoice(inv proofstore.Invoice) (string, error) {
	if inv.ChannelID == 0 {
		return "", errors.New("channeld: QR invoices must pin a channel (the payer is offline)")
	}
	// The invoice's own pin qualifier names the deployment; records
	// predating it re-resolve the bare id fail-closed. Never guess from the
	// registries config — its map order pairs one deployment's chain id
	// with another's address.
	key := proofstore.ChannelKey{ChainID: inv.ChainID, Registry: inv.Registry, ChannelID: inv.ChannelID}
	if inv.Registry == (util.Address{}) {
		resolved, err := n.ChannelKeyByID(inv.ChannelID)
		if err != nil {
			return "", err
		}
		key = resolved
	}
	if _, err := n.Store.Meta(key); err != nil {
		return "", fmt.Errorf("channeld: unknown channel %d on registry %s", key.ChannelID, key.Registry.Hex())
	}
	idBytes, err := hex.DecodeString(inv.ID)
	if err != nil || len(idBytes) != 16 {
		return "", errors.New("channeld: bad invoice id")
	}
	return qrenc.Encode(qrenc.Envelope{
		Type:      qrenc.TypeInvoice,
		Registry:  key.Registry,
		ChainID:   env64(key.ChainID),
		ChannelID: key.ChannelID,
		Amount:    inv.AmountWei.BigInt(),
		InvoiceID: idBytes,
		Expiry:    uint64(inv.ExpiresAt),
		EVMAddr:   inv.Merchant,
		Memo:      inv.Memo,
	})
}

// ScanQR dispatches a scanned code to the right handler by type.
func (n *Node) ScanQR(code string) (QRResult, error) {
	env, err := qrenc.Decode(strings.TrimSpace(code))
	if err != nil {
		return QRResult{}, err
	}
	switch env.Type {
	case qrenc.TypeInvoice:
		return n.qrPay(env)
	case qrenc.TypeProposal:
		return n.qrCountersign(env)
	case qrenc.TypeAck:
		return n.qrComplete(env)
	case qrenc.TypeCoopCloseProposal:
		return n.qrCloseCountersign(env)
	case qrenc.TypeCoopCloseAck:
		return n.qrCloseComplete(env)
	default:
		return QRResult{}, fmt.Errorf("channeld: unknown QR type %d", env.Type)
	}
}

// qrPay is the payer's step 2: scan the invoice, build and W1-persist the
// next state, display the proposal code.
func (n *Node) qrPay(env qrenc.Envelope) (QRResult, error) {
	key, meta, err := n.keyFor(env)
	if err != nil {
		return QRResult{}, err
	}
	if meta.PeerAddress != env.EVMAddr {
		return QRResult{}, errors.New("channeld: invoice merchant is not this channel's counterparty")
	}
	if time.Now().Unix() > int64(env.Expiry) {
		return QRResult{}, errors.New("channeld: invoice expired")
	}

	prop, err := n.Engine.ProposePayment(key, env.Amount, hex.EncodeToString(env.InvoiceID), n.bestKnownBlock(key))
	if err != nil {
		return QRResult{}, err
	}
	st, err := protocol.FromWire(prop.State)
	if err != nil {
		return QRResult{}, err
	}
	code, err := qrenc.Encode(qrenc.Envelope{
		Type:      qrenc.TypeProposal,
		Registry:  key.Registry,
		ChainID:   env64(key.ChainID),
		ChannelID: key.ChannelID,
		Seq:       st.Seq,
		TAB:       st.TransferredAtoB.BigInt(),
		TBA:       st.TransferredBtoA.BigInt(),
		Sig1:      st.SigOf(meta.Role),
		InvoiceID: env.InvoiceID,
	})
	if err != nil {
		return QRResult{}, err
	}
	return QRResult{
		Next:    code,
		Summary: fmt.Sprintf("paying %s wei on channel %d (seq %d); show this code to the merchant", env.Amount, key.ChannelID, st.Seq),
	}, nil
}

// qrCountersign is the merchant's step 3: validate, countersign (W2), and
// display the ACK code.
func (n *Node) qrCountersign(env qrenc.Envelope) (QRResult, error) {
	key, meta, err := n.keyFor(env)
	if err != nil {
		return QRResult{}, err
	}
	msg := protocol.ProposalMsg{
		V:            1,
		InvoiceID:    hex.EncodeToString(env.InvoiceID),
		State:        qrStateToWire(key, env, peerRoleOf(meta)),
		ProposerRole: string(peerRoleOf(meta)),
	}
	res, err := n.Engine.HandleProposal(msg, meta.PeerNpub, n.bestKnownBlock(key), time.Now().Unix())
	if err != nil {
		return QRResult{}, err
	}
	if res.Nack != nil {
		return QRResult{}, fmt.Errorf("channeld: refused: %s (%s)", res.Nack.Reason, res.Nack.Detail)
	}
	if res.Ack == nil {
		return QRResult{}, errors.New("channeld: proposal produced no ack")
	}

	mySig := util.FromHex(res.Ack.Sig)
	code, err := qrenc.Encode(qrenc.Envelope{
		Type:      qrenc.TypeAck,
		Registry:  key.Registry,
		ChainID:   env64(key.ChainID),
		ChannelID: key.ChannelID,
		Seq:       env.Seq,
		TAB:       env.TAB,
		TBA:       env.TBA,
		Sig1:      env.Sig1,
		Sig2:      mySig,
	})
	if err != nil {
		return QRResult{}, err
	}
	summary := fmt.Sprintf("payment received on channel %d (seq %d); show this code back to the payer", key.ChannelID, env.Seq)
	return QRResult{Next: code, Summary: summary, Complete: res.Completed}, nil
}

// qrComplete is the payer's step 4: verify the countersignature; the payment
// is COMPLETE only now.
func (n *Node) qrComplete(env qrenc.Envelope) (QRResult, error) {
	key, meta, err := n.keyFor(env)
	if err != nil {
		return QRResult{}, err
	}
	pending := proofstore.SignedState{
		Key:             key,
		Seq:             env.Seq,
		TransferredAtoB: proofstore.NewU256(env.TAB),
		TransferredBtoA: proofstore.NewU256(env.TBA),
		LockedAmount:    proofstore.NewU256(nil),
	}
	digest, err := pending.Digest()
	if err != nil {
		return QRResult{}, err
	}
	_ = meta
	ack := protocol.AckMsg{
		V:         1,
		ChannelID: strconv.FormatUint(key.ChannelID, 10),
		Seq:       strconv.FormatUint(env.Seq, 10),
		StateHash: digest.Hex(),
		Sig:       "0x" + util.Bytes2Hex(env.Sig2),
	}
	complete, err := n.Engine.HandleAck(key, ack)
	if err != nil {
		return QRResult{}, err
	}
	return QRResult{
		Summary:  fmt.Sprintf("payment COMPLETE at seq %d on channel %d", complete.Seq, key.ChannelID),
		Complete: complete,
	}, nil
}

// qrCloseCountersign handles a type-4 offline cooperative-close proposal.
func (n *Node) qrCloseCountersign(env qrenc.Envelope) (QRResult, error) {
	key, meta, err := n.keyFor(env)
	if err != nil {
		return QRResult{}, err
	}
	msg := protocol.CoopCloseProposalMsg{
		V:           1,
		ChannelID:   strconv.FormatUint(key.ChannelID, 10),
		Registry:    strings.ToLower(key.Registry.Hex()),
		ChainID:     key.ChainID,
		BalanceA:    env.BalA.String(),
		BalanceB:    env.BalB.String(),
		ExpiryBlock: strconv.FormatUint(env.Expiry, 10),
		Sig:         "0x" + util.Bytes2Hex(env.Sig1),
	}
	// The confirmed-scan watermark is a lower bound on the real head, so an
	// expiry at or below it is already past: countersigning would re-arm a
	// freeze the chain has released, and a live close can never be falsely
	// rejected here (expiry <= watermark <= head).
	if wm := n.bestKnownBlock(key); wm > 0 && env.Expiry <= wm {
		return QRResult{}, fmt.Errorf("channeld: cooperative close expired at block %d (last scanned block %d)", env.Expiry, wm)
	}
	// The engine still runs at nowBlock 0: a lagging watermark would falsely
	// trip the close-horizon bound on a legitimate near-horizon expiry, so
	// the horizon skip is the accepted offline residual (Part 2 §11).
	res, ready, err := n.Engine.HandleCoopCloseProposal(msg, meta.PeerNpub, 0)
	if err != nil {
		return QRResult{}, err
	}
	if res.Nack != nil {
		return QRResult{}, fmt.Errorf("channeld: refused: %s (%s)", res.Nack.Reason, res.Nack.Detail)
	}
	code, err := qrenc.Encode(qrenc.Envelope{
		Type:      qrenc.TypeCoopCloseAck,
		Registry:  key.Registry,
		ChainID:   env64(key.ChainID),
		ChannelID: key.ChannelID,
		Expiry:    env.Expiry,
		BalA:      env.BalA,
		BalB:      env.BalB,
		Sig1:      env.Sig1,
		Sig2:      util.FromHex(res.Ack.Sig),
	})
	if err != nil {
		return QRResult{}, err
	}
	_ = ready // submitted once back online (either side holds the pair)
	return QRResult{
		Next:    code,
		Summary: fmt.Sprintf("close countersigned for channel %d; submit on-chain when back online", key.ChannelID),
	}, nil
}

// qrCloseComplete handles the type-5 countersign on the proposer side.
func (n *Node) qrCloseComplete(env qrenc.Envelope) (QRResult, error) {
	key, _, err := n.keyFor(env)
	if err != nil {
		return QRResult{}, err
	}
	digest, err := coopCloseDigestFor(key, env)
	if err != nil {
		return QRResult{}, err
	}
	ack := protocol.AckMsg{
		V:         1,
		ChannelID: strconv.FormatUint(key.ChannelID, 10),
		Seq:       "0",
		StateHash: digest,
		Sig:       "0x" + util.Bytes2Hex(env.Sig2),
	}
	if _, err := n.Engine.HandleCoopCloseAck(key, ack); err != nil {
		return QRResult{}, err
	}
	return QRResult{
		Summary: fmt.Sprintf("cooperative close fully signed for channel %d; submit on-chain when back online", key.ChannelID),
	}, nil
}

// SubmitPendingCoopClose sends a fully countersigned close held in the store
// (the reconnect step after an offline type-4/5 exchange).
func (n *Node) SubmitPendingCoopClose(ctx context.Context, key proofstore.ChannelKey) error {
	meta, err := n.Store.Meta(key)
	if err != nil {
		return err
	}
	pc := meta.PendingClose
	if pc == nil || len(pc.MySig) != 65 || len(pc.PeerSig) != 65 {
		return protocol.ErrNoPendingClose
	}
	ready := &protocol.CoopCloseReady{
		Key:         key,
		BalanceA:    pc.BalanceA.BigInt(),
		BalanceB:    pc.BalanceB.BigInt(),
		ExpiryBlock: pc.ExpiryBlock,
	}
	if meta.Role == proofstore.RoleA {
		ready.SigA, ready.SigB = pc.MySig, pc.PeerSig
	} else {
		ready.SigA, ready.SigB = pc.PeerSig, pc.MySig
	}
	return n.SubmitCoopClose(ctx, ready)
}

// --------------------------------------------------------------- helpers

func peerRoleOf(meta proofstore.ChannelMeta) proofstore.Role {
	if meta.Role == proofstore.RoleA {
		return proofstore.RoleB
	}
	return proofstore.RoleA
}

// qrStateToWire rebuilds the wire state from an envelope, placing the lone
// signature in the proposer's slot.
func qrStateToWire(key proofstore.ChannelKey, env qrenc.Envelope, proposer proofstore.Role) protocol.WireState {
	w := protocol.WireState{
		ChannelID:       strconv.FormatUint(key.ChannelID, 10),
		Registry:        strings.ToLower(key.Registry.Hex()),
		ChainID:         key.ChainID,
		Seq:             strconv.FormatUint(env.Seq, 10),
		TransferredAtoB: env.TAB.String(),
		TransferredBtoA: env.TBA.String(),
		LocksRoot:       util.Hash{}.Hex(),
		LockedAmount:    "0",
	}
	sig := "0x" + util.Bytes2Hex(env.Sig1)
	if proposer == proofstore.RoleA {
		w.SigA = sig
	} else {
		w.SigB = sig
	}
	return w
}

func coopCloseDigestFor(key proofstore.ChannelKey, env qrenc.Envelope) (string, error) {
	chainID, ok := new(big.Int).SetString(key.ChainID, 10)
	if !ok {
		return "", fmt.Errorf("channeld: bad chain id %q", key.ChainID)
	}
	d := registry.Domain{ChainID: chainID, Registry: key.Registry}
	return d.HashCooperativeClose(new(big.Int).SetUint64(key.ChannelID), env.BalA, env.BalB, env.Expiry).Hex(), nil
}

func env64(chainID string) uint64 {
	v, _ := strconv.ParseUint(chainID, 10, 64)
	return v
}

