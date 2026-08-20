package nostrmod

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/nbd-wtf/go-nostr"

	"github.com/ParallaxProtocol/parallax/v2/wallet/channels/proofstore"
)

// Snapshot is the 21910 self-backup payload (Part 2 §6.10): all channel
// metadata, latest complete states, outstanding self-signed states, and the
// confirmed funding view. Written after every completed state change,
// gift-wrapped to the wallet's own npub. This is what shrinks total storage
// loss from catastrophic to inconvenient (Part 1 §9.2).
type Snapshot struct {
	V         int               `json:"v"`
	CreatedAt int64             `json:"createdAt"` // payload field; envelope timestamps are randomized
	Channels  []ChannelSnapshot `json:"channels"`
}

// ChannelSnapshot is one channel's full recoverable state.
type ChannelSnapshot struct {
	Meta       proofstore.ChannelMeta   `json:"meta"`
	Latest     *proofstore.SignedState  `json:"latest,omitempty"`
	SelfSigned []proofstore.SignedState `json:"selfSigned,omitempty"`
	Deposits   proofstore.Deposits      `json:"deposits"`
}

// maxSeq is the snapshot-freshness measure for one channel: the highest seq
// this snapshot knows about, complete or self-signed.
func (c *ChannelSnapshot) maxSeq() uint64 {
	var max uint64
	if c.Latest != nil {
		max = c.Latest.Seq
	}
	for _, st := range c.SelfSigned {
		if st.Seq > max {
			max = st.Seq
		}
	}
	return max
}

// BuildSnapshot reads the full recoverable state out of the store.
func BuildSnapshot(store *proofstore.Store, nowUnix int64) (Snapshot, error) {
	snap := Snapshot{V: 1, CreatedAt: nowUnix}
	metas, err := store.ListChannels()
	if err != nil {
		return snap, err
	}
	for _, meta := range metas {
		cs := ChannelSnapshot{Meta: meta}
		if latest, err := store.LatestState(meta.Key); err == nil {
			cs.Latest = &latest
		} else if !errors.Is(err, proofstore.ErrNotFound) {
			return snap, err
		}
		if cs.SelfSigned, err = store.SelfSigned(meta.Key); err != nil {
			return snap, err
		}
		if cs.Deposits, err = store.Deposits(meta.Key); err != nil {
			return snap, err
		}
		snap.Channels = append(snap.Channels, cs)
	}
	return snap, nil
}

// PublishBackup builds the current snapshot, wraps it to the wallet's own
// npub, and fans it out. Returns how many relays accepted the wrap; zero
// means the backup is NOT parked anywhere remote yet.
func PublishBackup(ctx context.Context, store *proofstore.Store, pool Publisher, nostrPriv string) (int, error) {
	selfPub, err := nostr.GetPublicKey(nostrPriv)
	if err != nil {
		return 0, err
	}
	snap, err := BuildSnapshot(store, time.Now().Unix())
	if err != nil {
		return 0, err
	}
	content, err := json.Marshal(snap)
	if err != nil {
		return 0, err
	}
	rumor := nostr.Event{
		Kind:      KindSelfBackup,
		Content:   string(content),
		CreatedAt: nostr.Timestamp(snap.CreatedAt),
	}
	wrap, err := Wrap(rumor, selfPub, nostrPriv)
	if err != nil {
		return 0, err
	}
	return pool.Publish(ctx, wrap), nil
}

// KindSelfBackup is the 21910 rumor kind (Part 2 §3).
const KindSelfBackup = 21910

// ParseBackup unwraps and parses one candidate backup wrap. Only wraps
// sealed by the wallet's own key are accepted — a relay cannot feed us
// someone else's (or a forged) snapshot.
func ParseBackup(wrap nostr.Event, nostrPriv string) (Snapshot, error) {
	var snap Snapshot
	selfPub, err := nostr.GetPublicKey(nostrPriv)
	if err != nil {
		return snap, err
	}
	rumor, sender, err := Unwrap(wrap, nostrPriv)
	if err != nil {
		return snap, err
	}
	if sender != selfPub {
		return snap, fmt.Errorf("nostrmod: backup sealed by %s, not self", sender)
	}
	if rumor.Kind != KindSelfBackup {
		return snap, fmt.Errorf("nostrmod: not a self-backup (kind %d)", rumor.Kind)
	}
	if err := json.Unmarshal([]byte(rumor.Content), &snap); err != nil {
		return snap, fmt.Errorf("nostrmod: backup parse: %w", err)
	}
	return snap, nil
}

// RestoreSnapshots merges snapshots into a store, taking the freshest view
// of each channel (max seq, ties broken by snapshot CreatedAt). Restoring
// re-journals self-signed states so W3 protection resumes exactly where it
// left off.
//
// The caller MUST reconcile against the chain (run a watcher pass over
// CloseStarted/Settled for every restored channel) before signing anything
// new (Part 3 §4.3).
func RestoreSnapshots(store *proofstore.Store, snapshots []Snapshot) (restored int, err error) {
	type best struct {
		cs        ChannelSnapshot
		seq       uint64
		createdAt int64
	}
	byKey := make(map[proofstore.ChannelKey]best)
	for _, snap := range snapshots {
		for _, cs := range snap.Channels {
			seq := cs.maxSeq()
			cur, ok := byKey[cs.Meta.Key]
			if !ok || seq > cur.seq || (seq == cur.seq && snap.CreatedAt > cur.createdAt) {
				byKey[cs.Meta.Key] = best{cs: cs, seq: seq, createdAt: snap.CreatedAt}
			}
		}
	}

	for _, b := range byKey {
		cs := b.cs
		if err := store.CreateChannel(cs.Meta); err != nil {
			if errors.Is(err, proofstore.ErrExists) {
				continue // restore never overwrites live local state
			}
			return restored, err
		}
		if cs.Latest != nil {
			if err := store.PutComplete(*cs.Latest); err != nil {
				return restored, err
			}
		}
		for _, st := range cs.SelfSigned {
			if err := store.PutSelfSigned(st); err != nil {
				return restored, err
			}
		}
		if err := store.PutDeposits(cs.Meta.Key, cs.Deposits); err != nil {
			return restored, err
		}
		restored++
	}
	return restored, nil
}
