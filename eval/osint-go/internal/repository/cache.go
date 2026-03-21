package repository

import (
	"context"
	"sync"
	"time"

	"github.com/yusefmosiah/fase/eval/osint-go/internal/model"
)

type Cache struct {
	mu       sync.RWMutex
	ttl      time.Duration
	items    map[string]cacheItem
	stopOnce sync.Once
	stopCh   chan struct{}
}

type cacheItem struct {
	result  model.ScanResult
	expires time.Time
}

func NewCache(ttl time.Duration) *Cache {
	c := &Cache{
		ttl:    ttl,
		items:  make(map[string]cacheItem),
		stopCh: make(chan struct{}),
	}
	go c.janitor()
	return c
}

func (c *Cache) Close() {
	c.stopOnce.Do(func() {
		close(c.stopCh)
	})
}

func (c *Cache) Save(_ context.Context, result model.ScanResult) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.items[result.ID] = cacheItem{
		result:  result,
		expires: time.Now().Add(c.ttl),
	}
}

func (c *Cache) Get(_ context.Context, id string) (model.ScanResult, bool) {
	c.mu.RLock()
	item, ok := c.items[id]
	c.mu.RUnlock()
	if !ok {
		return model.ScanResult{}, false
	}
	if time.Now().After(item.expires) {
		c.mu.Lock()
		delete(c.items, id)
		c.mu.Unlock()
		return model.ScanResult{}, false
	}
	return item.result, true
}

func (c *Cache) janitor() {
	if c.ttl <= 0 {
		return
	}
	interval := c.ttl / 2
	if interval <= 0 {
		interval = c.ttl
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			now := time.Now()
			c.mu.Lock()
			for id, item := range c.items {
				if now.After(item.expires) {
					delete(c.items, id)
				}
			}
			c.mu.Unlock()
		case <-c.stopCh:
			return
		}
	}
}
