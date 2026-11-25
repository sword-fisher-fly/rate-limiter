package service

import (
	"context"
	"path/filepath"
	"testing"

	"gitee.com/fisher_devops_cicd/rate-limiter-middleware/internal/repo/model"
	miniredis "github.com/alicebob/miniredis/v2"
	"github.com/glebarez/sqlite"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

// newTestService returns a rate limit config service that works on an empty
// database and on an in memory redis server.
func newTestService(t *testing.T) (*Service, *redis.Client) {
	t.Helper()

	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})

	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "rate-limiter.db")), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open the database: %v", err)
	}
	if err := db.AutoMigrate(&model.RateLimitConfig{}); err != nil {
		t.Fatalf("failed to create the table: %v", err)
	}

	return New(db, client), client
}

func TestServiceSyncsRedis(t *testing.T) {
	svc, client := newTestService(t)
	ctx := context.Background()

	cfg := &model.RateLimitConfig{
		Path:     "/api/v1/order/pay",
		ClientID: "client_001",
		IP:       "192.168.11.2",
		Rate:     10.5,
		Burst:    100,
	}
	if err := svc.Create(ctx, cfg); err != nil {
		t.Fatalf("failed to create the config: %v", err)
	}
	if cfg.ID <= 0 {
		t.Fatalf("the id of the config is %d", cfg.ID)
	}

	fields, err := client.HGetAll(ctx, "rl:cfg:/api/v1/order/pay:client_001:192.168.11.2").Result()
	if err != nil {
		t.Fatalf("failed to read the rule: %v", err)
	}
	if fields["rate"] != "10.5" || fields["burst"] != "100" {
		t.Fatalf("the rule in redis is %v", fields)
	}

	members, err := client.SMembers(ctx, ConfigIndexKey).Result()
	if err != nil {
		t.Fatalf("failed to read the index: %v", err)
	}
	if len(members) != 1 || members[0] != "rl:cfg:/api/v1/order/pay:client_001:192.168.11.2" {
		t.Fatalf("the index of the configs is %v", members)
	}

	// The client id of the config changes, so the rule moves to another key.
	if err := svc.Update(ctx, cfg.ID, &model.RateLimitConfig{
		Path:     "/api/v1/order/pay",
		ClientID: "client_002",
		IP:       "192.168.11.2",
		Rate:     20,
		Burst:    50,
	}); err != nil {
		t.Fatalf("failed to update the config: %v", err)
	}

	fields, err = client.HGetAll(ctx, "rl:cfg:/api/v1/order/pay:client_002:192.168.11.2").Result()
	if err != nil {
		t.Fatalf("failed to read the rule: %v", err)
	}
	if fields["rate"] != "20" {
		t.Fatalf("the rule in redis is %v", fields)
	}

	fields, err = client.HGetAll(ctx, "rl:cfg:/api/v1/order/pay:client_001:192.168.11.2").Result()
	if err != nil {
		t.Fatalf("failed to read the rule: %v", err)
	}
	if len(fields) != 0 {
		t.Fatalf("the old rule is still in redis: %v", fields)
	}

	// The path of a config can not be edited.
	if err := svc.Update(ctx, cfg.ID, &model.RateLimitConfig{
		Path:  "/api/v1/order/refund",
		Rate:  20,
		Burst: 50,
	}); err != ErrPathImmutable {
		t.Fatalf("the error of the update is %v, want %v", err, ErrPathImmutable)
	}

	// The ip of the config is cleared, so the rule moves to the key of the
	// wildcard and the empty ip is written to the database.
	if err := svc.Update(ctx, cfg.ID, &model.RateLimitConfig{
		Path:     "/api/v1/order/pay",
		ClientID: "client_002",
		Rate:     20,
		Burst:    50,
	}); err != nil {
		t.Fatalf("failed to update the config: %v", err)
	}

	fields, err = client.HGetAll(ctx, "rl:cfg:/api/v1/order/pay:client_002:_any_").Result()
	if err != nil {
		t.Fatalf("failed to read the rule: %v", err)
	}
	if fields["rate"] != "20" {
		t.Fatalf("the rule in redis is %v", fields)
	}

	stored, err := svc.Get(ctx, cfg.ID)
	if err != nil {
		t.Fatalf("failed to get the config: %v", err)
	}
	if stored.IP != "" {
		t.Fatalf("the ip of the config is %q, want an empty one", stored.IP)
	}

	// The config is removed from the database and from redis.
	if err := svc.Delete(ctx, cfg.ID); err != nil {
		t.Fatalf("failed to delete the config: %v", err)
	}

	fields, err = client.HGetAll(ctx, "rl:cfg:/api/v1/order/pay:client_002:_any_").Result()
	if err != nil {
		t.Fatalf("failed to read the rule: %v", err)
	}
	if len(fields) != 0 {
		t.Fatalf("the rule is still in redis: %v", fields)
	}

	members, err = client.SMembers(ctx, ConfigIndexKey).Result()
	if err != nil {
		t.Fatalf("failed to read the index: %v", err)
	}
	if len(members) != 0 {
		t.Fatalf("the index is not empty: %v", members)
	}
}

func TestServiceRejectsDuplicate(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	cfg := &model.RateLimitConfig{
		Path:     "/api/v1/order/pay",
		ClientID: "client_001",
		IP:       "192.168.11.2",
		Rate:     10,
		Burst:    100,
	}
	if err := svc.Create(ctx, cfg); err != nil {
		t.Fatalf("failed to create the config: %v", err)
	}

	// A config with the same path, client id and ip would share the rule of
	// the config that exists already.
	if err := svc.Create(ctx, &model.RateLimitConfig{
		Path:     "/api/v1/order/pay",
		ClientID: "client_001",
		IP:       "192.168.11.2",
		Rate:     20,
		Burst:    50,
	}); err != ErrDuplicate {
		t.Fatalf("the error of the create is %v, want %v", err, ErrDuplicate)
	}

	// The client id is compared in the case insensitive form the key of the
	// rule is built from.
	if err := svc.Create(ctx, &model.RateLimitConfig{
		Path:     "/api/v1/order/pay",
		ClientID: "CLIENT_001",
		IP:       "192.168.11.2",
		Rate:     20,
		Burst:    50,
	}); err != ErrDuplicate {
		t.Fatalf("the error of the create is %v, want %v", err, ErrDuplicate)
	}

	// A config of another client is a rule of its own.
	if err := svc.Create(ctx, &model.RateLimitConfig{
		Path:     "/api/v1/order/pay",
		ClientID: "client_002",
		IP:       "192.168.11.2",
		Rate:     20,
		Burst:    50,
	}); err != nil {
		t.Fatalf("failed to create the config: %v", err)
	}

	// A config of another path is a rule of its own.
	if err := svc.Create(ctx, &model.RateLimitConfig{
		Path:     "/api/v1/order/refund",
		ClientID: "client_001",
		IP:       "192.168.11.2",
		Rate:     20,
		Burst:    50,
	}); err != nil {
		t.Fatalf("failed to create the config: %v", err)
	}
}
