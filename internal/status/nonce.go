package status

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"
)

const (
	nonceTTL  = 60 * time.Second
	maxNonces = 8
)

// Nonces are the short-lived, single-use values that go into login links, so
// the long-lived secret never appears in a command line or a URL.
type Nonces struct {
	Now func() time.Time // replaceable in tests

	mu      sync.Mutex
	expires map[string]time.Time
}

func NewNonces() *Nonces {
	return &Nonces{Now: time.Now, expires: map[string]time.Time{}}
}

func (n *Nonces) purgeLocked(now time.Time) {
	for v, exp := range n.expires {
		if !now.Before(exp) {
			delete(n.expires, v)
		}
	}
}

// New returns a fresh nonce valid for 60 seconds; at most 8 stay open, the oldest goes first.
func (n *Nonces) New() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	v := hex.EncodeToString(raw)
	n.mu.Lock()
	defer n.mu.Unlock()
	now := n.Now()
	n.purgeLocked(now)
	for len(n.expires) >= maxNonces {
		var oldest string
		for k, exp := range n.expires {
			if oldest == "" || exp.Before(n.expires[oldest]) {
				oldest = k
			}
		}
		delete(n.expires, oldest)
	}
	n.expires[v] = now.Add(nonceTTL)
	return v, nil
}

// Consume accepts a nonce exactly once.
func (n *Nonces) Consume(v string) bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	now := n.Now()
	n.purgeLocked(now)
	_, ok := n.expires[v]
	delete(n.expires, v)
	return ok
}
