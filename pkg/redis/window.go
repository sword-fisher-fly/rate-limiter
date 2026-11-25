package redis

import (
	"context"
	"fmt"
	"math/rand"
	"strconv"
	"time"

	redis "github.com/redis/go-redis/v9"
)

// IncrWindow records an event in the sliding window kept at key and returns
// the number of events recorded during the trailing window.
//
// Events are stored in a sorted set scored by their arrival time and the
// entries older than the window are dropped on every call, so the set only
// holds the events of the current window. A one second window therefore
// yields the request rate per second.
func IncrWindow(ctx context.Context, r Redis, key string, window time.Duration) (int64, error) {
	now := time.Now()
	from := strconv.FormatInt(now.Add(-window).UnixMilli(), 10)

	// The three commands run in one transaction, so the count is computed
	// on the window that already contains the event being recorded.
	pipe := r.TxPipeline()
	pipe.ZAdd(ctx, key, redis.Z{Score: float64(now.UnixMilli()), Member: eventID(now)})
	pipe.ZRemRangeByScore(ctx, key, "-inf", from)
	count := pipe.ZCount(ctx, key, from, "+inf")

	if _, err := pipe.Exec(ctx); err != nil {
		return 0, fmt.Errorf("failed to record the event in window %s: %v", key, err)
	}

	return count.Val(), nil
}

// eventID builds an identifier that is unique for every event, even when
// two instances record an event at the very same moment.
func eventID(now time.Time) string {
	return fmt.Sprintf("%d-%d", now.UnixNano(), rand.Int63())
}
