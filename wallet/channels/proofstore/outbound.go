package proofstore

import (
	"encoding/json"

	bolt "go.etcd.io/bbolt"
)

// OutboundItem is one queued rumor awaiting delivery. The rumor content is
// stored, not the wrap: retransmission reuses the identical rumor under a
// fresh seal/wrap each attempt (Part 2 §7.2). The queue is persistent so
// retransmission schedules survive restarts (Part 3 §6).
type OutboundItem struct {
	ID          uint64     `json:"id"`
	DedupeKey   string     `json:"dedupeKey"` // removal handle, e.g. "21902:<channel>:<seq>"
	ToNpub      string     `json:"toNpub"`    // x-only hex
	Kind        int        `json:"kind"`
	Content     string     `json:"content"`        // rumor content JSON
	Tags        [][]string `json:"tags,omitempty"` // rumor tags (inside the encryption)
	RumorTime   int64      `json:"rumorTime"`      // rumor created_at, stable across attempts
	ExpiresAt   int64      `json:"expiresAt"`      // unix; give-up deadline
	NextAttempt int64      `json:"nextAttempt"`    // unix; 0 = immediately
	Attempts    int        `json:"attempts"`
}

var bucketOutbound = []byte("outbound")

// EnqueueOutbound persists a delivery item and returns its id.
func (s *Store) EnqueueOutbound(item OutboundItem) (uint64, error) {
	var id uint64
	err := s.db.Update(func(tx *bolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists(bucketOutbound)
		if err != nil {
			return err
		}
		id, err = b.NextSequence()
		if err != nil {
			return err
		}
		item.ID = id
		raw, err := json.Marshal(item)
		if err != nil {
			return err
		}
		return b.Put(seqKey(id), raw)
	})
	return id, err
}

// DueOutbound returns items whose NextAttempt has passed, oldest first, up
// to limit. Items past their ExpiresAt are deleted and returned separately —
// the caller decides the give-up consequence (a 21902 give-up marks the
// channel poisoned, Part 3 §5).
func (s *Store) DueOutbound(nowUnix int64, limit int) (due, expired []OutboundItem, err error) {
	err = s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketOutbound)
		if b == nil {
			return nil
		}
		c := b.Cursor()
		for k, v := c.First(); k != nil; k, v = c.Next() {
			var item OutboundItem
			if err := json.Unmarshal(v, &item); err != nil {
				return err
			}
			if nowUnix > item.ExpiresAt {
				expired = append(expired, item)
				if err := c.Delete(); err != nil {
					return err
				}
				continue
			}
			if item.NextAttempt <= nowUnix && len(due) < limit {
				due = append(due, item)
			}
		}
		return nil
	})
	return due, expired, err
}

// RescheduleOutbound records an attempt and the next retry time.
func (s *Store) RescheduleOutbound(id uint64, attempts int, nextAttempt int64) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketOutbound)
		if b == nil {
			return ErrNotFound
		}
		raw := b.Get(seqKey(id))
		if raw == nil {
			return ErrNotFound
		}
		var item OutboundItem
		if err := json.Unmarshal(raw, &item); err != nil {
			return err
		}
		item.Attempts = attempts
		item.NextAttempt = nextAttempt
		out, err := json.Marshal(item)
		if err != nil {
			return err
		}
		return b.Put(seqKey(id), out)
	})
}

// RemoveOutbound deletes one item by id (delivered, or superseded).
func (s *Store) RemoveOutbound(id uint64) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketOutbound)
		if b == nil {
			return nil
		}
		return b.Delete(seqKey(id))
	})
}

// RemoveOutboundByDedupe deletes every item with the given dedupe key
// (e.g. the proposal a just-arrived ACK settles) and reports how many.
func (s *Store) RemoveOutboundByDedupe(key string) (int, error) {
	removed := 0
	err := s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketOutbound)
		if b == nil {
			return nil
		}
		c := b.Cursor()
		for k, v := c.First(); k != nil; k, v = c.Next() {
			var item OutboundItem
			if err := json.Unmarshal(v, &item); err != nil {
				return err
			}
			if item.DedupeKey == key {
				if err := c.Delete(); err != nil {
					return err
				}
				removed++
			}
		}
		return nil
	})
	return removed, err
}

// OutboundLen reports the queue size (metrics).
func (s *Store) OutboundLen() (int, error) {
	n := 0
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketOutbound)
		if b == nil {
			return nil
		}
		return b.ForEach(func(_, _ []byte) error { n++; return nil })
	})
	return n, err
}
