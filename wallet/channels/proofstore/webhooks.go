package proofstore

import (
	"encoding/json"

	bolt "go.etcd.io/bbolt"
)

// WebhookItem is one pending merchant webhook delivery (Part 3 §9:
// at-least-once with backoff). Persisted so retries survive restarts.
type WebhookItem struct {
	ID          uint64 `json:"id"`
	URL         string `json:"url"`
	Body        []byte `json:"body"` // JSON payload, signed by the caller
	NextAttempt int64  `json:"nextAttempt"`
	Attempts    int    `json:"attempts"`
	ExpiresAt   int64  `json:"expiresAt"` // give-up deadline
}

var bucketWebhooks = []byte("webhooks")

// EnqueueWebhook persists a delivery item.
func (s *Store) EnqueueWebhook(item WebhookItem) (uint64, error) {
	var id uint64
	err := s.db.Update(func(tx *bolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists(bucketWebhooks)
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

// DueWebhooks returns deliveries whose retry time has passed; expired items
// are dropped and returned separately for logging.
func (s *Store) DueWebhooks(nowUnix int64, limit int) (due, expired []WebhookItem, err error) {
	err = s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketWebhooks)
		if b == nil {
			return nil
		}
		c := b.Cursor()
		for k, v := c.First(); k != nil; k, v = c.Next() {
			var item WebhookItem
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

// RescheduleWebhook records a failed attempt.
func (s *Store) RescheduleWebhook(id uint64, attempts int, nextAttempt int64) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketWebhooks)
		if b == nil {
			return ErrNotFound
		}
		raw := b.Get(seqKey(id))
		if raw == nil {
			return ErrNotFound
		}
		var item WebhookItem
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

// RemoveWebhook deletes a delivered item.
func (s *Store) RemoveWebhook(id uint64) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketWebhooks)
		if b == nil {
			return nil
		}
		return b.Delete(seqKey(id))
	})
}
