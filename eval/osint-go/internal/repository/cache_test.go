package repository

import (
	"context"
	"testing"
	"time"

	"github.com/yusefmosiah/fase/eval/osint-go/internal/model"
)

func TestCacheTTLExpiry(t *testing.T) {
	t.Parallel()

	cache := NewCache(25 * time.Millisecond)
	t.Cleanup(cache.Close)

	cache.Save(context.Background(), model.ScanResult{ID: "abc", Domain: "example.com"})
	if _, ok := cache.Get(context.Background(), "abc"); !ok {
		t.Fatalf("expected cache hit")
	}

	time.Sleep(50 * time.Millisecond)
	if _, ok := cache.Get(context.Background(), "abc"); ok {
		t.Fatalf("expected cache miss after ttl")
	}
}
