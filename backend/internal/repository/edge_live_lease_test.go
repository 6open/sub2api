//go:build unit

package repository

import (
	"context"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"testing"
)

func TestEdgeLiveLeaseSurvivesProcessCleanupAndCountsAgainstNormal(t *testing.T) {
	mini := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mini.Addr()})
	defer client.Close()
	cache := NewConcurrencyCache(client, 15, 60)
	live := cache.(service.LiveConcurrencyCache)
	ctx := context.Background()
	ok, err := live.AcquireLiveLease(ctx, 6, 1, 1, 1, 108, "edge:test", false)
	if err != nil || !ok {
		t.Fatal(err)
	}
	if err := cache.CleanupStaleProcessSlots(ctx, "new-process:"); err != nil {
		t.Fatal(err)
	}
	ok, err = cache.AcquireAccountSlot(ctx, 6, 1, "regular")
	if err != nil || ok {
		t.Fatal("normal request ignored edge lease")
	}
	ok, err = cache.AcquireUserSlot(ctx, 1, 1, "regular")
	if err != nil || ok {
		t.Fatal("user limit ignored edge lease")
	}
	if err := live.ReleaseLiveLease(ctx, 6, 1, 108, "edge:test"); err != nil {
		t.Fatal(err)
	}
	ok, err = cache.AcquireAccountSlot(ctx, 6, 1, "regular")
	if err != nil || !ok {
		t.Fatal("released lease still blocks")
	}
}
