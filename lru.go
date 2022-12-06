package lru

import (
	"sync"

	"github.com/sentioxyz/golang-lru/simplelru"
)

const (
	// DefaultEvictedBufferSize defines the default buffer size to store evicted key/val
	DefaultEvictedBufferSize = 16
)

// Cache is a thread-safe fixed size LRU cache.
type Cache[K comparable, V any] struct {
	lru         *simplelru.LRU[K, V]
	evictedKeys []K
	evictedVals []V
	onEvictedCB func(k K, v V)
	lock        sync.RWMutex
}

// New creates an LRU of the given size.
func New[K comparable, V any](size int) (*Cache[K, V], error) {
	return NewWithEvict[K, V](size, nil)
}

// NewWithEvict constructs a fixed size cache with the given eviction
// callback.
func NewWithEvict[K comparable, V any](size int, onEvicted func(key K, value V)) (c *Cache[K, V], err error) {
	return NewWithWeightLimitAndEvict(size, 0, nil, onEvicted)
}

// NewWithWeightLimitAndEvict constructs a fixed size cache with the weight limit and given eviction callback.
func NewWithWeightLimitAndEvict[K comparable, V any](
	size int,
	weightLimit uint64,
	weightCalculator func(value V) uint64,
	onEvicted func(key K, value V),
) (c *Cache[K, V], err error) {
	// create a cache with default settings
	c = &Cache[K, V]{
		onEvictedCB: onEvicted,
	}
	if onEvicted != nil {
		c.initEvictBuffers()
		onEvicted = c.onEvicted
	}
	c.lru, err = simplelru.NewLRUWithWeightLimit(size, weightLimit, weightCalculator, onEvicted)
	return
}

func (c *Cache[K, V]) initEvictBuffers() {
	c.evictedKeys = make([]K, 0, DefaultEvictedBufferSize)
	c.evictedVals = make([]V, 0, DefaultEvictedBufferSize)
}

// onEvicted save evicted key/val and sent in externally registered callback
// outside of critical section
func (c *Cache[K, V]) onEvicted(k K, v V) {
	c.evictedKeys = append(c.evictedKeys, k)
	c.evictedVals = append(c.evictedVals, v)
}

func (c *Cache[K, V]) collectEvicted(purge bool) (evictedKeys []K, evictedVals []V) {
	count := len(c.evictedKeys)
	if count == 0 {
		return
	}
	if purge {
		evictedKeys, evictedVals = c.evictedKeys, c.evictedVals
		c.initEvictBuffers()
	} else {
		evictedKeys, evictedVals = make([]K, count), make([]V, count)
		copy(evictedKeys, c.evictedKeys)
		copy(evictedVals, c.evictedVals)
		c.evictedKeys, c.evictedVals = c.evictedKeys[:0], c.evictedVals[:0]
	}
	return
}

func (c *Cache[K, V]) callEvictCB(evictedKeys []K, evictedVals []V) {
	for i := 0; i < len(evictedKeys); i++ {
		c.onEvictedCB(evictedKeys[i], evictedVals[i])
	}
}

// Purge is used to completely clear the cache.
func (c *Cache[K, V]) Purge() {
	c.lock.Lock()
	c.lru.Purge()
	if c.onEvictedCB != nil && len(c.evictedKeys) > 0 {
		ks, vs := c.collectEvicted(true)
		// invoke callback outside of critical section
		defer c.callEvictCB(ks, vs)
	}
	c.lock.Unlock()
}

// Add adds a value to the cache. Returns true if an eviction occurred.
func (c *Cache[K, V]) Add(key K, value V) (evicted bool) {
	c.lock.Lock()
	evicted = c.lru.Add(key, value)
	if c.onEvictedCB != nil && evicted {
		ks, vs := c.collectEvicted(false)
		// invoke callback outside of critical section
		defer c.callEvictCB(ks, vs)
	}
	c.lock.Unlock()
	return
}

// Get looks up a key's value from the cache.
func (c *Cache[K, V]) Get(key K) (value V, ok bool) {
	c.lock.Lock()
	value, ok = c.lru.Get(key)
	c.lock.Unlock()
	return value, ok
}

// Contains checks if a key is in the cache, without updating the
// recent-ness or deleting it for being stale.
func (c *Cache[K, V]) Contains(key K) bool {
	c.lock.RLock()
	containKey := c.lru.Contains(key)
	c.lock.RUnlock()
	return containKey
}

// Peek returns the key value (or undefined if not found) without updating
// the "recently used"-ness of the key.
func (c *Cache[K, V]) Peek(key K) (value V, ok bool) {
	c.lock.RLock()
	value, ok = c.lru.Peek(key)
	c.lock.RUnlock()
	return value, ok
}

// GetOrAdd looks up a key's value from the cache, if not exist, adds the value.
// Returns previous value, whether found and whether an eviction occurred.
func (c *Cache[K, V]) GetOrAdd(key K, value V) (previous V, ok, evicted bool) {
	c.lock.Lock()
	previous, ok = c.lru.Get(key)
	if ok {
		c.lock.Unlock()
		return
	}
	evicted = c.lru.Add(key, value)
	if c.onEvictedCB != nil && evicted {
		ks, vs := c.collectEvicted(false)
		// invoke callback outside of critical section
		defer c.callEvictCB(ks, vs)
	}
	c.lock.Unlock()
	return previous, ok, evicted
}

// ContainsOrAdd checks if a key is in the cache without updating the
// recent-ness or deleting it for being stale, and if not, adds the value.
// Returns whether found and whether an eviction occurred.
func (c *Cache[K, V]) ContainsOrAdd(key K, value V) (ok, evicted bool) {
	c.lock.Lock()
	if c.lru.Contains(key) {
		c.lock.Unlock()
		return true, false
	}
	evicted = c.lru.Add(key, value)
	if c.onEvictedCB != nil && evicted {
		ks, vs := c.collectEvicted(false)
		// invoke callback outside of critical section
		defer c.callEvictCB(ks, vs)
	}
	c.lock.Unlock()
	return false, evicted
}

// PeekOrAdd checks if a key is in the cache without updating the
// recent-ness or deleting it for being stale, and if not, adds the value.
// Returns whether found and whether an eviction occurred.
func (c *Cache[K, V]) PeekOrAdd(key K, value V) (previous V, ok, evicted bool) {
	c.lock.Lock()
	previous, ok = c.lru.Peek(key)
	if ok {
		c.lock.Unlock()
		return previous, true, false
	}
	evicted = c.lru.Add(key, value)
	if c.onEvictedCB != nil && evicted {
		ks, vs := c.collectEvicted(false)
		// invoke callback outside of critical section
		defer c.callEvictCB(ks, vs)
	}
	c.lock.Unlock()
	return
}

// Remove removes the provided key from the cache.
func (c *Cache[K, V]) Remove(key K) (present bool) {
	c.lock.Lock()
	present = c.lru.Remove(key)
	if c.onEvictedCB != nil && present {
		ks, vs := c.collectEvicted(false)
		// invoke callback outside of critical section
		defer c.callEvictCB(ks, vs)
	}
	c.lock.Unlock()
	return
}

// Resize changes the cache size.
func (c *Cache[K, V]) Resize(size int) (evicted int) {
	c.lock.Lock()
	evicted = c.lru.Resize(size)
	if c.onEvictedCB != nil && evicted > 0 {
		ks, vs := c.collectEvicted(true)
		// invoke callback outside of critical section
		defer c.callEvictCB(ks, vs)
	}
	c.lock.Unlock()
	return evicted
}

// ResetWeightLimit changes the weight limit.
func (c *Cache[K, V]) ResetWeightLimit(weightLimit uint64) (evicted int) {
	c.lock.Lock()
	evicted = c.lru.ResetWeightLimit(weightLimit)
	if c.onEvictedCB != nil && evicted > 0 {
		ks, vs := c.collectEvicted(true)
		// invoke callback outside of critical section
		defer c.callEvictCB(ks, vs)
	}
	c.lock.Unlock()
	return evicted
}

// RemoveOldest removes the oldest item from the cache.
func (c *Cache[K, V]) RemoveOldest() (key K, value V, ok bool) {
	c.lock.Lock()
	key, value, ok = c.lru.RemoveOldest()
	if c.onEvictedCB != nil && ok {
		ks, vs := c.collectEvicted(true)
		// invoke callback outside of critical section
		defer c.callEvictCB(ks, vs)
	}
	c.lock.Unlock()
	return
}

// GetOldest returns the oldest entry
func (c *Cache[K, V]) GetOldest() (key K, value V, ok bool) {
	c.lock.RLock()
	key, value, ok = c.lru.GetOldest()
	c.lock.RUnlock()
	return
}

// Keys returns a slice of the keys in the cache, from oldest to newest.
func (c *Cache[K, V]) Keys() []K {
	c.lock.RLock()
	keys := c.lru.Keys()
	c.lock.RUnlock()
	return keys
}

// Len returns the number of items in the cache.
func (c *Cache[K, V]) Len() int {
	c.lock.RLock()
	length := c.lru.Len()
	c.lock.RUnlock()
	return length
}

// WeightTotal returns the sum of the weight of all the entries in the cache.
func (c *Cache[K, V]) WeightTotal() uint64 {
	c.lock.RLock()
	weightTotal := c.lru.WeightTotal()
	c.lock.RUnlock()
	return weightTotal
}
