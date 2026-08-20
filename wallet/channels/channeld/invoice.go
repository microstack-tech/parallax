package channeld

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"math/big"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/ParallaxProtocol/parallax/v2/util"
	"github.com/ParallaxProtocol/parallax/v2/wallet/channels/proofstore"
	"github.com/ParallaxProtocol/parallax/v2/wallet/channels/protocol"
)

// DefaultInvoiceTTL bounds an invoice with no explicit TTL.
const DefaultInvoiceTTL = 15 * time.Minute

// CreateInvoice mints and persists an invoice (merchant side, Part 2 §6.1)
// and returns it with its bootstrap payment URI. channelID zero leaves the
// payer free to pick any shared open channel.
func (n *Node) CreateInvoice(amountWei *big.Int, memo string, ttl time.Duration, channelID uint64) (proofstore.Invoice, string, error) {
	if amountWei == nil || amountWei.Sign() <= 0 {
		return proofstore.Invoice{}, "", fmt.Errorf("channeld: non-positive invoice amount")
	}
	if ttl <= 0 {
		ttl = DefaultInvoiceTTL
	}
	var idBytes [16]byte
	if _, err := rand.Read(idBytes[:]); err != nil {
		return proofstore.Invoice{}, "", err
	}
	inv := proofstore.Invoice{
		ID:        hex.EncodeToString(idBytes[:]),
		AmountWei: proofstore.NewU256(amountWei),
		Memo:      memo,
		ExpiresAt: time.Now().Add(ttl).Unix(),
		ChannelID: channelID,
		Merchant:  n.Signer.Address(),
	}
	if err := n.Store.CreateInvoice(inv); err != nil {
		return proofstore.Invoice{}, "", err
	}
	return inv, n.InvoiceURI(inv), nil
}

// InvoiceURI renders the bootstrap URI (Part 2 §6.1):
// parallax:<evmAddress>?amount=<wei>&inv=<id>&npub=<hex>&relays=<r1|r2>&reg=<registry>
func (n *Node) InvoiceURI(inv proofstore.Invoice) string {
	var reg string
	for _, entries := range n.Cfg.Registries {
		if len(entries) > 0 {
			reg = strings.ToLower(entries[0].Address)
			break
		}
	}
	q := url.Values{}
	q.Set("amount", inv.AmountWei.BigInt().String())
	q.Set("inv", inv.ID)
	q.Set("npub", n.SelfPub)
	q.Set("relays", strings.Join(n.Cfg.Nostr.Relays, "|"))
	q.Set("reg", reg)
	return "parallax:" + strings.ToLower(n.Signer.Address().Hex()) + "?" + q.Encode()
}

// PaymentRequest is a parsed payment URI.
type PaymentRequest struct {
	Merchant  util.Address
	AmountWei *big.Int
	InvoiceID string
	Npub      string
	Relays    []string
	Registry  util.Address
}

// ParsePaymentURI parses a parallax: URI.
func ParsePaymentURI(uri string) (PaymentRequest, error) {
	var req PaymentRequest
	rest, ok := strings.CutPrefix(uri, "parallax:")
	if !ok {
		return req, fmt.Errorf("channeld: not a parallax: URI")
	}
	addr, query, _ := strings.Cut(rest, "?")
	if !util.IsHexAddress(addr) {
		return req, fmt.Errorf("channeld: bad merchant address %q", addr)
	}
	req.Merchant = util.HexToAddress(addr)
	q, err := url.ParseQuery(query)
	if err != nil {
		return req, err
	}
	if req.AmountWei, ok = new(big.Int).SetString(q.Get("amount"), 10); !ok || req.AmountWei.Sign() <= 0 {
		return req, fmt.Errorf("channeld: bad amount %q", q.Get("amount"))
	}
	req.InvoiceID = q.Get("inv")
	req.Npub = q.Get("npub")
	if relays := q.Get("relays"); relays != "" {
		req.Relays = strings.Split(relays, "|")
	}
	if reg := q.Get("reg"); reg != "" {
		if !util.IsHexAddress(reg) {
			return req, fmt.Errorf("channeld: bad registry %q", reg)
		}
		req.Registry = util.HexToAddress(reg)
	}
	return req, nil
}

// SendInvoice queues a 21901 to a channel counterparty (for payers already
// reachable over the relay; the URI covers cold bootstrap).
func (n *Node) SendInvoice(key proofstore.ChannelKey, inv proofstore.Invoice) error {
	meta, err := n.Store.Meta(key)
	if err != nil {
		return err
	}
	msg := protocol.InvoiceMsg{
		V:          1,
		InvoiceID:  inv.ID,
		Amount:     inv.AmountWei.BigInt().String(),
		EVMAddress: strings.ToLower(n.Signer.Address().Hex()),
		Registry:   strings.ToLower(key.Registry.Hex()),
		ChainID:    key.ChainID,
		Memo:       inv.Memo,
		ExpiresAt:  strconv.FormatInt(inv.ExpiresAt, 10),
		Relays:     n.Cfg.Nostr.Relays,
	}
	if inv.ChannelID != 0 {
		msg.ChannelID = strconv.FormatUint(inv.ChannelID, 10)
	}
	content, err := protocol.EncodePayload(msg)
	if err != nil {
		return err
	}
	now := time.Now().Unix()
	_, err = n.Store.EnqueueOutbound(proofstore.OutboundItem{
		DedupeKey: "21901:" + inv.ID,
		ToNpub:    meta.PeerNpub,
		Kind:      protocol.KindInvoice,
		Content:   content,
		Tags:      [][]string{{"inv", inv.ID}},
		RumorTime: now,
		ExpiresAt: inv.ExpiresAt,
	})
	return err
}

// handleInvoice records an inbound 21901 on the payer side (Part 2 §6.1
// payer checks run at pay time) so `channel pay --invoice <id>` can resolve
// amount and channel without retyping.
func (n *Node) handleInvoice(msg protocol.InvoiceMsg, sender string) error {
	amount, ok := new(big.Int).SetString(msg.Amount, 10)
	if !ok || amount.Sign() <= 0 {
		return fmt.Errorf("channeld: bad invoice amount %q", msg.Amount)
	}
	expires, err := strconv.ParseInt(msg.ExpiresAt, 10, 64)
	if err != nil {
		return fmt.Errorf("channeld: bad invoice expiry %q", msg.ExpiresAt)
	}
	if !util.IsHexAddress(msg.EVMAddress) {
		return fmt.Errorf("channeld: bad invoice merchant address")
	}
	merchant := util.HexToAddress(msg.EVMAddress)

	// Only accept invoices from a known channel counterparty whose npub and
	// address both match — anything else is unsolicited spam.
	metas, err := n.Store.ListChannels()
	if err != nil {
		return err
	}
	known := false
	var channelID uint64
	for _, meta := range metas {
		if meta.PeerNpub == sender && meta.PeerAddress == merchant {
			known = true
			channelID = meta.Key.ChannelID
			break
		}
	}
	if !known {
		return fmt.Errorf("channeld: invoice from unknown counterparty")
	}
	if msg.ChannelID != "" {
		if pinned, err := strconv.ParseUint(msg.ChannelID, 10, 64); err == nil {
			channelID = pinned
		}
	}

	inv := proofstore.Invoice{
		ID:        msg.InvoiceID,
		AmountWei: proofstore.NewU256(amount),
		Memo:      msg.Memo,
		ExpiresAt: expires,
		ChannelID: channelID,
		Merchant:  merchant,
	}
	if err := n.Store.CreateInvoice(inv); err != nil {
		if err == proofstore.ErrExists {
			return nil // retransmission
		}
		return err
	}
	n.log.Info("invoice received", "id", inv.ID, "amountWei", amount, "memo", msg.Memo)
	return nil
}

// PayInvoice resolves a locally stored invoice (received via 21901) and pays
// it on its channel.
func (n *Node) PayInvoice(ctx context.Context, invoiceID string) error {
	inv, err := n.Store.Invoice(invoiceID)
	if err != nil {
		return fmt.Errorf("channeld: unknown invoice %s (was it received over the relay?)", invoiceID)
	}
	if time.Now().Unix() > inv.ExpiresAt {
		return fmt.Errorf("channeld: invoice expired")
	}
	key, ok := n.channelByID(strconv.FormatUint(inv.ChannelID, 10))
	if !ok {
		return fmt.Errorf("channeld: no channel with the invoice's merchant")
	}
	return n.Pay(ctx, key, inv.AmountWei.BigInt(), inv.ID)
}
