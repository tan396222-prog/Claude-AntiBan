//go:build linux || darwin || windows

package reversesshfs

import (
	"errors"
	"io/fs"
	"time"
)

// noopRemovalTTL bounds how long an ExpectRemove token waits for the guest.
const noopRemovalTTL = 5 * time.Second

// ownRemovalTTL bounds how long a removal made by the server hides the host's event for it.
const ownRemovalTTL = 2 * time.Second

// maxRelayEntries bounds each table, whatever the host does.
const maxRelayEntries = 1 << 20

// expiring is a set of keys that each expire ttl after being added. Entries
// are queued in the order they expire, so purging costs O(1) per entry.
type expiring struct {
	ttl   time.Duration
	m     map[string]time.Time
	q     []expiringKey
	front int
}

type expiringKey struct {
	key    string
	expiry time.Time
}

func newExpiring(ttl time.Duration) *expiring {
	return &expiring{ttl: ttl, m: make(map[string]time.Time)}
}

// add reports false when the set is full.
func (e *expiring) add(key string, now time.Time) bool {
	e.purge(now)
	if len(e.q)-e.front >= maxRelayEntries {
		return false
	}
	expiry := now.Add(e.ttl)
	e.m[key] = expiry
	e.q = append(e.q, expiringKey{key, expiry})
	return true
}

func (e *expiring) has(key string, now time.Time) bool {
	e.purge(now)
	_, ok := e.m[key]
	return ok
}

// remove reports whether key was there. Its queue entry stays until it expires.
func (e *expiring) remove(key string, now time.Time) bool {
	ok := e.has(key, now)
	delete(e.m, key)
	return ok
}

func (e *expiring) purge(now time.Time) {
	for e.front < len(e.q) && !now.Before(e.q[e.front].expiry) {
		k := e.q[e.front]
		// A key added again has a later expiry, and its own queue entry.
		if e.m[k.key].Equal(k.expiry) {
			delete(e.m, k.key)
		}
		e.q[e.front] = expiringKey{}
		e.front++
	}
	if e.front >= 1024 && e.front*2 >= len(e.q) {
		e.q = append(e.q[:0], e.q[e.front:]...)
		e.front = 0
	}
}

// relay is the state behind ExpectRemove.
//
// The hostagent relays a deletion made on the host by having the guest remove the path, which
// makes the guest emit an event. The server must then answer that removal without touching the host:
// the path may have been created again there since. Two things must not be mistaken for it:
// the host's event for a removal that the guest itself asked for, and a removal of a path the
// guest created after the host deleted it.
type relay struct {
	tokens *expiring // paths whose next removal is the relayed one
	own    *expiring // paths the server just removed or renamed away
}

func newRelay() *relay {
	return &relay{tokens: newExpiring(noopRemovalTTL), own: newExpiring(ownRemovalTTL)}
}

// expectRemove makes the next Remove or Rmdir request for p, within noopRemovalTTL, succeed without
// touching the host, and reports true. p is a host path under the root. It reports false, and
// the removal must not be relayed, when the server removed p itself just now.
func (h *rootedHandlers) expectRemove(p string) bool {
	rel, err := h.rel(slashPath(p))
	if err != nil {
		return false
	}
	now := h.now()
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.relay.own.has(rel, now) {
		return false
	}
	return h.relay.tokens.add(rel, now)
}

func (h *rootedHandlers) consumeNoopRemoval(p string) bool {
	rel, err := h.rel(p)
	if err != nil {
		return false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.relay.tokens.remove(rel, h.now())
}

// removing records that the server is about to remove or move away p, a request path.
// This is before the removal, as the host's event can come right after it.
func (h *rootedHandlers) removing(p string) {
	rel, err := h.rel(p)
	if err != nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.relay.own.add(rel, h.now())
}

// removalFailed takes back removing(p) when the removal or move did not happen: nothing of the host's
// changed, so a later deletion of p there is not the server's own. A path that was not there stays: an
// earlier removal of it may be the one the host's event is about.
func (h *rootedHandlers) removalFailed(p string, err error) error {
	if err == nil || errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if rel, rerr := h.rel(p); rerr == nil {
		h.mu.Lock()
		h.relay.own.remove(rel, h.now())
		h.mu.Unlock()
	}
	return err
}

// creating records that the server is about to create p, a request path: a removal of it that follows
// is the guest's own, not a relayed one.
func (h *rootedHandlers) creating(p string) {
	rel, err := h.rel(p)
	if err != nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.relay.tokens.remove(rel, h.now())
}
