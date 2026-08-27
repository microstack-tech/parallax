package channeld

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/ParallaxProtocol/parallax/v2/util"
	"github.com/ParallaxProtocol/parallax/v2/wallet/channels/proofstore"
)

// ErrAmbiguousChannel: a bare channel id matched channels on more than one
// coexisting registry; the caller must use the qualified
// <chainId>:<registry>:<id> form.
var ErrAmbiguousChannel = errors.New("channeld: channel id exists on multiple registries; use <chainId>:<registry>:<id>")

// ChannelKeyByID resolves a user-supplied bare channel id, failing when
// coexisting registries both know it — acting on the first match could
// freeze or force-close the wrong channel.
func (n *Node) ChannelKeyByID(id uint64) (proofstore.ChannelKey, error) {
	metas, err := n.Store.ListChannels()
	if err != nil {
		return proofstore.ChannelKey{}, err
	}
	var found []proofstore.ChannelKey
	for _, meta := range metas {
		if meta.Key.ChannelID == id {
			found = append(found, meta.Key)
		}
	}
	switch len(found) {
	case 0:
		return proofstore.ChannelKey{}, fmt.Errorf("channeld: unknown channel %d", id)
	case 1:
		return found[0], nil
	default:
		return proofstore.ChannelKey{}, fmt.Errorf("%w (channel %d)", ErrAmbiguousChannel, id)
	}
}

// ParseChannelRef resolves a user-supplied channel reference: a bare decimal
// id, or the qualified "<chainId>:<registry>:<id>" form (ChannelKey.String)
// when ids collide across coexisting registries.
func (n *Node) ParseChannelRef(ref string) (proofstore.ChannelKey, error) {
	if !strings.Contains(ref, ":") {
		id, err := strconv.ParseUint(ref, 10, 64)
		if err != nil {
			return proofstore.ChannelKey{}, fmt.Errorf("channeld: bad channel id %q", ref)
		}
		return n.ChannelKeyByID(id)
	}
	parts := strings.Split(ref, ":")
	if len(parts) != 3 || !util.IsHexAddress(parts[1]) {
		return proofstore.ChannelKey{}, fmt.Errorf("channeld: bad channel reference %q (want <chainId>:<registry>:<id>)", ref)
	}
	if _, err := strconv.ParseUint(parts[0], 10, 64); err != nil {
		return proofstore.ChannelKey{}, fmt.Errorf("channeld: bad chain id in %q", ref)
	}
	id, err := strconv.ParseUint(parts[2], 10, 64)
	if err != nil {
		return proofstore.ChannelKey{}, fmt.Errorf("channeld: bad channel id in %q", ref)
	}
	key := proofstore.ChannelKey{ChainID: parts[0], Registry: util.HexToAddress(parts[1]), ChannelID: id}
	if _, err := n.Store.Meta(key); err != nil {
		return proofstore.ChannelKey{}, fmt.Errorf("channeld: unknown channel %s", ref)
	}
	return key, nil
}

// ChannelSummary is one channel's operator-facing row: identity, roles,
// latest seq, and the close balances at the current view. Shared by the
// merchant daemon's API and the CLI listing.
type ChannelSummary struct {
	ChannelID uint64 `json:"channelId"`
	Registry  string `json:"registry"`
	ChainID   string `json:"chainId"`
	Role      string `json:"role"`
	Status    string `json:"status"`
	Peer      string `json:"peer"`
	Seq       uint64 `json:"seq"`
	BalanceA  string `json:"balanceA"`
	BalanceB  string `json:"balanceB"`
	Poisoned  bool   `json:"poisoned,omitempty"`
	FrozenTil uint64 `json:"frozenUntilBlock,omitempty"`
}

// ChannelSummaries renders every stored channel as a summary row.
func (n *Node) ChannelSummaries() ([]ChannelSummary, error) {
	metas, err := n.Store.ListChannels()
	if err != nil {
		return nil, err
	}
	rows := make([]ChannelSummary, 0, len(metas))
	for _, meta := range metas {
		row := ChannelSummary{
			ChannelID: meta.Key.ChannelID,
			Registry:  meta.Key.Registry.Hex(),
			ChainID:   meta.Key.ChainID,
			Role:      string(meta.Role),
			Status:    string(meta.Status),
			Peer:      meta.PeerAddress.Hex(),
			Poisoned:  meta.Poisoned,
			FrozenTil: meta.FrozenUntilBlock,
		}
		if latest, err := n.Store.LatestState(meta.Key); err == nil {
			row.Seq = latest.Seq
		}
		if balA, balB, err := n.Engine.CloseBalances(meta.Key); err == nil {
			row.BalanceA, row.BalanceB = balA.String(), balB.String()
		}
		rows = append(rows, row)
	}
	return rows, nil
}
