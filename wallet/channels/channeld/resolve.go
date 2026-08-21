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
