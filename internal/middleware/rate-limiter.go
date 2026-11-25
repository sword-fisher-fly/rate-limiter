package middleware

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"gitee.com/fisher_devops_cicd/rate-limiter-middleware/internal/service"
	"gitee.com/fisher_devops_cicd/rate-limiter-middleware/pkg/redis"
	"github.com/gin-gonic/gin"
	goredis "github.com/redis/go-redis/v9"
	"github.com/toolkits/pkg/logger"
)

const (
	// ClientIDHeader is the header the client id of a request is taken from.
	ClientIDHeader = "X-Client-Id"
	// RemainingHeader holds the number of the tokens the most exhausted
	// bucket of the matched rules still has.
	RemainingHeader = "X-RateLimit-Remaining"
)

// tokenBucketScript refills the token bucket of a rate limit config and takes
// the token of the request from it. The script runs on the redis server, so
// the requests of every instance of the service are counted in one shared
// bucket.
//
// The bucket holds burst tokens at most and is refilled with rate tokens per
// second. A request that finds no token in the bucket is rejected, the
// script reports how many seconds the client has to wait for the bucket to
// hold a token again.
var tokenBucketScript = goredis.NewScript(`
local key = KEYS[1]
local now = tonumber(ARGV[1])
local rate = tonumber(ARGV[2])
local burst = tonumber(ARGV[3])
local requested = tonumber(ARGV[4])

-- A rule without a rate does not limit anything, so the request is allowed.
if rate == nil or rate <= 0 or burst == nil or burst < 0 then
    return {1, 0, 0}
end

local data = redis.call('HMGET', key, 'tokens', 'last_refill')
local tokens = tonumber(data[1])
local last_refill = tonumber(data[2])

-- A bucket that is used for the first time starts full.
if tokens == nil then tokens = burst end
if last_refill == nil then last_refill = now end

-- The bucket is refilled with the tokens of the time that passed since the
-- last request, it never holds more than burst tokens.
local elapsed = math.max(0, now - last_refill)
tokens = math.min(burst, tokens + elapsed * rate / 1000.0)

local allowed, retry_after = 0, 0
if tokens >= requested then
    tokens = tokens - requested
    allowed = 1
else
    -- The number of the seconds the client has to wait for the tokens the
    -- bucket is missing.
    retry_after = math.ceil((requested - tokens) / rate)
    if retry_after < 1 then retry_after = 1 end
end

redis.call('HSET', key, 'tokens', tokens, 'last_refill', now)
-- The bucket of an idle rule is dropped, so the next request of the rule
-- starts with a full bucket again.
redis.call('EXPIRE', key, math.ceil(burst / rate) + 1)

return {allowed, math.floor(tokens), retry_after}
`)

// Rule is a rate limit rule, it is read from the redis hash of a rate limit
// config.
type Rule struct {
	// Key is the key of the redis hash of the config the rule belongs to.
	Key string
	// Rate is the number of the requests allowed per second.
	Rate float64
	// Burst is the number of the requests allowed at once.
	Burst int
}

// Result is the outcome of the rate limit check of a request.
type Result struct {
	// Allowed tells whether the request may be served.
	Allowed bool
	// Remaining is the number of the tokens of the most exhausted bucket of
	// the matched rules, it is -1 when no rule matches the request.
	Remaining int
	// RetryAfter is the number of the seconds the client has to wait before
	// the request is allowed again.
	RetryAfter int
	// Rules is the number of the rules that matched the request.
	Rules int
}

// RateLimiter enforces the rate limit rules of the configs published in redis
// by the rate limit config service.
type RateLimiter struct {
	rdb redis.Redis
}

// NewRateLimiter returns a rate limiter that reads the rules from the given
// redis server.
func NewRateLimiter(rdb redis.Redis) *RateLimiter {
	return &RateLimiter{rdb: rdb}
}

// candidateKeys returns the keys of the configs that can match a request for
// the given path, client id and ip. The keys are ordered by their
// precedence, the most specific one first.
//
// 1. rl:cfg:{path}:{client_id}:{ip}          most specific
// 2. rl:cfg:{path}:{client_id}:_any_
// 3. rl:cfg:{path}:_any_:{ip}
// 4. rl:cfg:{path}:_any_:_any_                least specific
func candidateKeys(path, clientID, ip string) []string {
	keys := []string{
		service.ConfigKey(path, clientID, ip),
		service.ConfigKey(path, clientID, service.AnyValue),
		service.ConfigKey(path, service.AnyValue, ip),
		service.ConfigKey(path, service.AnyValue, service.AnyValue),
	}

	unique := make([]string, 0, len(keys))
	seen := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		unique = append(unique, key)
	}

	return unique
}

// ResolveRules returns the rules of every config that matches the request.
// The candidates are asked for in one round trip, in the order of their
// precedence.
func (rl *RateLimiter) ResolveRules(ctx context.Context, path, clientID, ip string) ([]Rule, error) {
	keys := candidateKeys(path, clientID, ip)

	pipe := rl.rdb.Pipeline()
	cmds := make([]*goredis.MapStringStringCmd, len(keys))
	for i, key := range keys {
		cmds[i] = pipe.HGetAll(ctx, key)
	}
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, goredis.Nil) {
		return nil, fmt.Errorf("failed to read the rate limit configs of %s: %v", path, err)
	}

	var rules []Rule
	for i, cmd := range cmds {
		fields, err := cmd.Result()
		if err != nil {
			if errors.Is(err, goredis.Nil) {
				continue
			}
			return nil, fmt.Errorf("failed to read the rate limit config %s: %v", keys[i], err)
		}
		if len(fields) == 0 {
			continue
		}

		rule, err := parseRule(keys[i], fields)
		if err != nil {
			return nil, err
		}
		rules = append(rules, *rule)
	}

	return rules, nil
}

// Check enforces the rules of every config that matches the request. Every
// matched rule has its own token bucket and consumes a token of it, the
// request is allowed only when all the buckets allow it.
//
// A request that matches no rule is not limited.
func (rl *RateLimiter) Check(ctx context.Context, path, clientID, ip string) (Result, error) {
	rules, err := rl.ResolveRules(ctx, path, clientID, ip)
	if err != nil {
		return Result{}, err
	}

	if len(rules) == 0 {
		return Result{Allowed: true, Remaining: -1}, nil
	}

	result := Result{Allowed: true, Remaining: -1, Rules: len(rules)}
	for _, rule := range rules {
		allowed, remaining, retryAfter, err := rl.takeToken(ctx, rule)
		if err != nil {
			return Result{}, err
		}

		if !allowed {
			result.Allowed = false
		}
		if result.Remaining < 0 || remaining < result.Remaining {
			result.Remaining = remaining
		}
		if retryAfter > result.RetryAfter {
			result.RetryAfter = retryAfter
		}
	}

	return result, nil
}

// takeToken takes the token of the request from the bucket of the given rule
// and reports whether the bucket allowed the request.
func (rl *RateLimiter) takeToken(ctx context.Context, rule Rule) (bool, int, int, error) {
	res, err := tokenBucketScript.Run(ctx, rl.rdb,
		[]string{service.CounterKey(rule.Key)},
		time.Now().UnixMilli(), rule.Rate, rule.Burst, 1,
	).Slice()
	if err != nil {
		return false, 0, 0, fmt.Errorf("failed to take a token of the rate limit config %s: %v", rule.Key, err)
	}

	allowed, err := scriptInt(res, 0)
	if err != nil {
		return false, 0, 0, fmt.Errorf("failed to read the result of the rate limit config %s: %v", rule.Key, err)
	}

	remaining, err := scriptInt(res, 1)
	if err != nil {
		return false, 0, 0, fmt.Errorf("failed to read the result of the rate limit config %s: %v", rule.Key, err)
	}

	retryAfter, err := scriptInt(res, 2)
	if err != nil {
		return false, 0, 0, fmt.Errorf("failed to read the result of the rate limit config %s: %v", rule.Key, err)
	}

	return allowed == 1, remaining, retryAfter, nil
}

// parseRule reads the rule of a rate limit config from the fields of its
// redis hash.
func parseRule(key string, fields map[string]string) (*Rule, error) {
	rate, err := strconv.ParseFloat(fields[service.RuleFieldRate], 64)
	if err != nil {
		return nil, fmt.Errorf("the rate of the rate limit config %s is invalid: %v", key, err)
	}
	burst, err := strconv.Atoi(fields[service.RuleFieldBurst])
	if err != nil {
		return nil, fmt.Errorf("the burst of the rate limit config %s is invalid: %v", key, err)
	}

	return &Rule{Key: key, Rate: rate, Burst: burst}, nil
}

// scriptInt reads the number at the given position of the result of a lua
// script.
func scriptInt(res []interface{}, index int) (int, error) {
	if index >= len(res) {
		return 0, fmt.Errorf("the result of the script holds %d values, the value %d is missing", len(res), index)
	}

	value, ok := res[index].(int64)
	if !ok {
		return 0, fmt.Errorf("the value %d of the result of the script is not a number", index)
	}

	return int(value), nil
}

// RateLimit returns the middleware that enforces the rate limit rules of the
// configs of the service. A request that exceeds one of the matched rules is
// rejected with the status code 429 before it reaches the business logic.
func RateLimit(rl *RateLimiter) gin.HandlerFunc {
	return func(c *gin.Context) {
		path := c.Request.URL.Path
		clientID := c.GetHeader(ClientIDHeader)
		ip := c.ClientIP()

		result, err := rl.Check(c.Request.Context(), path, clientID, ip)
		if err != nil {
			// A rate limiter that cannot reach the redis server must not take
			// the business APIs down, so the request is allowed and the
			// failure is reported in the log.
			logger.Errorf("failed to check the rate limit of %s: %v", path, err)
			c.Next()
			return
		}

		if result.Remaining >= 0 {
			c.Header(RemainingHeader, strconv.Itoa(result.Remaining))
		}

		if !result.Allowed {
			c.Header("Retry-After", strconv.Itoa(result.RetryAfter))
			c.JSON(http.StatusTooManyRequests, gin.H{
				"code": http.StatusTooManyRequests,
				"msg":  "too many requests",
			})
			c.Abort()
			return
		}

		c.Next()
	}
}
