package b2

import (
	"container/ring"
	"sync"
	"time"

	"github.com/kurin/blazer/b2"
)

// b2cache is a simple cache implementation to reuse metadata from already retrieved objects.
// The implementation is rather naive: we keep a map from the blob id to our metadata object.
// We also remember the time we added the object. If it's too old, we can just pretend to not
// know about it.
// Additionally we use a ring buffer to keep the size at bay. We add each new item to the ring.
// When we come full circle and encounter an element that is already filled, we remove that
// element from the map.
type b2Cache struct {
	mtx     sync.Mutex
	entries map[string]*b2CacheEntry
	ring    *ring.Ring
}

type b2CacheEntry struct {
	o  *b2.Object
	ts time.Time
}

func newB2Cache(size int) *b2Cache {
	return &b2Cache{
		entries: make(map[string]*b2CacheEntry, size),
		ring:    ring.New(size),
	}
}

func (c *b2Cache) Add(id string, o *b2.Object) {
	c.mtx.Lock()
	defer c.mtx.Unlock()

	if c.ring.Value != nil {
		delete(c.entries, c.ring.Value.(string))
	}

	c.entries[id] = &b2CacheEntry{
		o:  o,
		ts: time.Now(),
	}

	c.ring.Value = id
	c.ring = c.ring.Next()
}

func (c *b2Cache) Get(id string) *b2.Object {
	c.mtx.Lock()
	defer c.mtx.Unlock()

	entry := c.entries[id]
	if entry == nil {
		return nil
	}

	if entry.ts.Before(time.Now().Add(-1 * time.Minute)) {
		// Older than a minute seems unreasonably outdated. Just forget it.
		delete(c.entries, id)
		return nil
	}

	return entry.o
}
