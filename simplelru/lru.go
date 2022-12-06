package simplelru

import (
	"errors"
)

// EvictCallback is used to get a callback when a cache entry is evicted
type EvictCallback[K comparable, V any] func(key K, value V)

// WeightCalculator is used to calculate the weight of a value
type WeightCalculator[V any] func(value V) uint64

// LRU implements a non-thread safe fixed size LRU cache
type LRU[K comparable, V any] struct {
	size      int
	evictList *lruList[K, V]
	items     map[K]*entry[K, V]
	onEvict   EvictCallback[K, V]

	weightTotal      uint64
	weightLimit      uint64
	weightCalculator WeightCalculator[V]
}

// NewLRU constructs an LRU of the given size
func NewLRU[K comparable, V any](size int, onEvict EvictCallback[K, V]) (*LRU[K, V], error) {
	return NewLRUWithWeightLimit(size, 0, nil, onEvict)
}

func NewLRUWithWeightLimit[K comparable, V any](
	size int,
	weightLimit uint64,
	weightCalculator WeightCalculator[V],
	onEvict EvictCallback[K, V],
) (*LRU[K, V], error) {
	if size <= 0 {
		return nil, errors.New("must provide a positive size")
	}

	c := &LRU[K, V]{
		size:      size,
		evictList: newList[K, V](),
		items:     make(map[K]*entry[K, V]),
		onEvict:   onEvict,

		weightLimit:      weightLimit,
		weightCalculator: weightCalculator,
	}
	return c, nil
}

// Purge is used to completely clear the cache.
func (c *LRU[K, V]) Purge() {
	for k, v := range c.items {
		if c.onEvict != nil {
			c.onEvict(k, v.value)
		}
		delete(c.items, k)
	}
	c.evictList.init()
}

// Add adds a value to the cache.  Returns true if an eviction occurred.
func (c *LRU[K, V]) Add(key K, value V) (evicted bool) {
	// Check for existing item
	if ent, ok := c.items[key]; ok {
		c.evictList.moveToFront(ent)
		ent.value = value

		if c.weightCalculator != nil {
			c.weightTotal -= ent.weight
			ent.weight = c.weightCalculator(value)
			c.weightTotal += ent.weight
		}
		return c.checkEvict() > 0
	}

	// Add new item
	ent := c.evictList.pushFront(key, value)
	c.items[key] = ent

	if c.weightCalculator != nil {
		ent.weight = c.weightCalculator(value)
		c.weightTotal += ent.weight
	}
	return c.checkEvict() > 0
}

// Get looks up a key's value from the cache.
func (c *LRU[K, V]) Get(key K) (value V, ok bool) {
	if ent, ok := c.items[key]; ok {
		c.evictList.moveToFront(ent)
		return ent.value, true
	}
	return
}

// Contains checks if a key is in the cache, without updating the recent-ness
// or deleting it for being stale.
func (c *LRU[K, V]) Contains(key K) (ok bool) {
	_, ok = c.items[key]
	return ok
}

// Peek returns the key value (or undefined if not found) without updating
// the "recently used"-ness of the key.
func (c *LRU[K, V]) Peek(key K) (value V, ok bool) {
	var ent *entry[K, V]
	if ent, ok = c.items[key]; ok {
		return ent.value, true
	}
	return
}

// Remove removes the provided key from the cache, returning if the
// key was contained.
func (c *LRU[K, V]) Remove(key K) (present bool) {
	if ent, ok := c.items[key]; ok {
		c.removeElement(ent)
		return true
	}
	return false
}

// RemoveOldest removes the oldest item from the cache.
func (c *LRU[K, V]) RemoveOldest() (key K, value V, ok bool) {
	if ent := c.evictList.back(); ent != nil {
		c.removeElement(ent)
		return ent.key, ent.value, true
	}
	return
}

// GetOldest returns the oldest entry
func (c *LRU[K, V]) GetOldest() (key K, value V, ok bool) {
	if ent := c.evictList.back(); ent != nil {
		return ent.key, ent.value, true
	}
	return
}

// Keys returns a slice of the keys in the cache, from oldest to newest.
func (c *LRU[K, V]) Keys() []K {
	keys := make([]K, c.evictList.length())
	i := 0
	for ent := c.evictList.back(); ent != nil; ent = ent.prevEntry() {
		keys[i] = ent.key
		i++
	}
	return keys
}

// Len returns the number of items in the cache.
func (c *LRU[K, V]) Len() int {
	return c.evictList.length()
}

// WeightTotal returns the sum of the weight of all the entries in the cache.
func (c *LRU[K, V]) WeightTotal() uint64 {
	return c.weightTotal
}

// Resize changes the cache size.
func (c *LRU[K, V]) Resize(size int) (evicted int) {
	c.size = size
	return c.checkEvict()
}

// ResetWeightLimit changes the weight limit.
func (c *LRU[K, V]) ResetWeightLimit(weightLimit uint64) (evicted int) {
	c.weightLimit = weightLimit
	return c.checkEvict()
}

// checkEvict removes the oldest items unit size and weightLimit are all safe
func (c *LRU[K, V]) checkEvict() int {
	var evicted int
	for c.evictList.length() > c.size || c.weightTotal > c.weightLimit {
		ent := c.evictList.back() // never be nil
		c.removeElement(ent)
		evicted++
	}
	return evicted
}

// removeElement is used to remove a given list element from the cache
func (c *LRU[K, V]) removeElement(e *entry[K, V]) {
	c.evictList.remove(e)
	delete(c.items, e.key)
	c.weightTotal -= e.weight
	if c.onEvict != nil {
		c.onEvict(e.key, e.value)
	}
}
