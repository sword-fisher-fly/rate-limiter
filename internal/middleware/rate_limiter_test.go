package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"gitee.com/fisher_devops_cicd/rate-limiter-middleware/internal/repo/model"
	"gitee.com/fisher_devops_cicd/rate-limiter-middleware/internal/service"
	miniredis "github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

func setup(t *testing.T) (*RateLimiter, *service.Service, *redis.Client) {
	t.Helper()

	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open the database: %v", err)
	}
	if err := db.AutoMigrate(&model.RateLimitConfig{}); err != nil {
		t.Fatalf("failed to create the table: %v", err)
	}

	return NewRateLimiter(client), service.New(db, client), client
}

func equalStrings(t *testing.T, name string, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s = %v, want %v", name, got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("%s = %v, want %v", name, got, want)
		}
	}
}

func TestCandidateKeys(t *testing.T) {
	keys := candidateKeys("/api/v1/order/pay", "client_001", "192.168.11.2")
	equalStrings(t, "candidateKeys", keys, []string{
		"rl:cfg:/api/v1/order/pay:client_001:192.168.11.2",
		"rl:cfg:/api/v1/order/pay:client_001:_any_",
		"rl:cfg:/api/v1/order/pay:_any_:192.168.11.2",
		"rl:cfg:/api/v1/order/pay:_any_:_any_",
	})

	// A client id that is not set is the wildcard itself, so the key is only
	// built once.
	keys = candidateKeys("/api/v1/order/pay", "", "192.168.11.2")
	equalStrings(t, "candidateKeys", keys, []string{
		"rl:cfg:/api/v1/order/pay:_any_:192.168.11.2",
		"rl:cfg:/api/v1/order/pay:_any_:_any_",
	})
}

func TestCheckWithoutRule(t *testing.T) {
	rl, _, _ := setup(t)

	result, err := rl.Check(context.Background(), "/api/v1/order/pay", "client_001", "192.168.11.2")
	if err != nil {
		t.Fatalf("failed to check the rate limit: %v", err)
	}
	if !result.Allowed || result.Rules != 0 {
		t.Fatalf("the request without a rule must be allowed, got %+v", result)
	}
}

func TestCheckLayered(t *testing.T) {
	rl, svc, _ := setup(t)
	ctx := context.Background()

	// A rule that matches every client of the path.
	if err := svc.Create(ctx, &model.RateLimitConfig{
		Path:  "/api/v1/order/pay",
		Rate:  1,
		Burst: 2,
	}); err != nil {
		t.Fatalf("failed to create the config: %v", err)
	}
	// A rule that matches the client only.
	if err := svc.Create(ctx, &model.RateLimitConfig{
		Path:     "/api/v1/order/pay",
		ClientID: "client_001",
		Rate:     100,
		Burst:    100,
	}); err != nil {
		t.Fatalf("failed to create the config: %v", err)
	}

	// Both rules match, the request is allowed while the buckets of both
	// rules hold a token.
	for i := 0; i < 2; i++ {
		result, err := rl.Check(ctx, "/api/v1/order/pay", "client_001", "192.168.11.2")
		if err != nil {
			t.Fatalf("failed to check the rate limit: %v", err)
		}
		if !result.Allowed || result.Rules != 2 {
			t.Fatalf("the request must be allowed, got %+v", result)
		}
	}

	// The bucket of the rule that matches every client is empty, so the
	// request is rejected although the bucket of the client rule is not.
	result, err := rl.Check(ctx, "/api/v1/order/pay", "client_001", "192.168.11.2")
	if err != nil {
		t.Fatalf("failed to check the rate limit: %v", err)
	}
	if result.Allowed || result.Rules != 2 || result.RetryAfter < 1 {
		t.Fatalf("the request must be rejected, got %+v", result)
	}

	// Another client does not match the rule of the client, but it still
	// matches the rule that matches every client of the path.
	result, err = rl.Check(ctx, "/api/v1/order/pay", "client_002", "192.168.11.2")
	if err != nil {
		t.Fatalf("failed to check the rate limit: %v", err)
	}
	if result.Allowed || result.Rules != 1 {
		t.Fatalf("the request must be rejected, got %+v", result)
	}
}

func TestMiddlewareRejectsRequest(t *testing.T) {
	rl, svc, _ := setup(t)
	ctx := context.Background()

	if err := svc.Create(ctx, &model.RateLimitConfig{
		Path:  "/api/v1/rate-limiter-mock",
		Rate:  1,
		Burst: 1,
	}); err != nil {
		t.Fatalf("failed to create the config: %v", err)
	}

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(RateLimit(rl))
	r.GET("/api/v1/rate-limiter-mock", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"code": 0, "msg": "ok"})
	})

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/rate-limiter-mock", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("the first request must be allowed, got %d", w.Code)
	}

	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/rate-limiter-mock", nil))
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("the second request must be rejected, got %d", w.Code)
	}
	if w.Header().Get("Retry-After") != "1" {
		t.Fatalf("the retry after header is %q, want 1", w.Header().Get("Retry-After"))
	}
}
