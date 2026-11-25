1. In the file `internal/api/rate-limit-mock.go`, add an API called `RateLimiterMock`, defined as belows
a) Method: `GET`, Path: `/api/v1/rate-limiter-mock`, record the total number of the request and calculate the request rate per second dynamically
Response
{
    "code": 0,
    "msg": "ok",
    "data": {
        "request_total": 2,
        "rate_per_second": 1
    }
}
b) register the API in the file `internal/router/route.go`

2. In the file `pkg/db/sqlite.go`, implement sqlite client object based on gorm library and sqlite database and init the tables defined in `migrations/sql/rate-limiter.sql`, register the database instance in the function `Initialize` located at `cmd/rate-limiter/rate-limiter-middleware.go`

3. replace the logger in the file `pkg/db/sqlite.go` with the logger `github.com/toolkits/pkg/logger`

4. refactor the file `pkg/db/orm.go` to support both sqlite and mysql as storage 

modify the database instance creation and registeration in the file `cmd/rate-limiter/rate-limiter-middleware.go`

5. change the field `rate` in both the file `migrations/mysql/rate-limiter.sql` and `migrations/sqlite/rate-limiter.sql` to the double type

======================================================================

6. File hierarchy is as the following
a) API layer: `internal/api/rate-limit-config.go`
b) Service layer: `internal/service/rate-limiter.go`
c) Ratelimiter and Middleware: `internal/middleware/rate-limiter.go`
d) Add middleware for business API except the rate limit configure API
e) Create or Update rate limit configure into Redis config key and index key when the user call the rate limit configure create or update API.
f) For business API, call the config key in Redis pipeline in precedence, return allow only when all the rules are satisfied.

Rate limit CRUD API describe as described in the following, for write operation, it should both update the record in database and redis in the service layer located at the folder `internal/service`
```Create Rate Limiter Config
POST /rate-limit/configs
Request parameter
{
    "path": "/api/v1/order/pay",
    "client_id": "client_001",
    "ip": "192.168.11.2",
    "rate": 10.0,
    "burst": 100
}
```

```Get Rate Limiter Config
GET /rate-limit/configs/:id
Request parameter: null
Response parameter
{
    "code": 0,
    "msg": "ok",
    "data": {
        "id": 1,
        "path": "/api/v1/order/pay",
        "client_id": "client_001",
        "ip": "192.168.11.2",
        "rate": 10.0,
        "burst": 100
    }
}
```

```List Rate Limiter Configs
GET /rate-limit/configs?page_size=10&page=1

Response parameter
{
    "code": 0,
    "msg": "ok",
    "data": {
        "items": [{
            "id": 1,
            "path": "/api/v1/order/pay",
            "client_id": "client_001",
            "ip": "192.168.11.2",
            "rate": 10.0,
            "burst": 100
        }],
        "total": 1
    }
}
```

```Update Rate Limiter Config
path field can not be edited.
PUT /rate-limit/configs/:id

Request Parameter
{
    "path": "/api/v1/order/pay",
    "client_id": "client_001",
    "ip": "192.168.11.2",
    "rate": 10.0,
    "burst": 100
}

Response Parameter
{
    "code": 0,
    "msg": "ok"
}
```

```Delete Rate Limiter Configs
DELETE /rate-limit/configs/:id

Request Parameter: null
Response Parameter:
{
    "code": 0,
    "msg": "ok"
}
```


Below code snippet is just for reference, fix the logic error if you find any unreasonable code.
The path field in the RateLimitConfig is required and add binding restriction.
```API Form
type RateLimitConfig struct {
    Name      string  `json:"name"`
    Path      string  `json:"path"`
    ClientID  string  `json:"client_id"`
    IP        string  `json:"ip"`
    Rate      float32 `json:"rate"`
    Burst     int     `json:"burst"`
}
```

Place the below code logic in the file `internal/middleware/rate-limiter.go`.

```Data Model
path is the required parameter, client_id and ip are optional.
Config hash:  rl:cfg:{path}:{client_id}:{ip}
  fields: rate, burst
Index set:    rl:cfg:index         (members = config keys, for listing)
Counter key:  rl:tb:{path}:{client_id}:{ip}   (derived from matched config key) what is the meaning of the key and for what purpose? 
```

``` Key parse and Precedence Matching
func candidateKeys(path, clientID, ip string) []string {
    p, c, i := norm(path), norm(clientID), norm(ip)
    return []string{
        fmt.Sprintf("rl:cfg:%s:%s:%s", p, c, i),
        fmt.Sprintf("rl:cfg:%s:%s:%s", p, c, "_any_"),
        fmt.Sprintf("rl:cfg:%s:%s:%s", p, "_any_", i),
        fmt.Sprintf("rl:cfg:%s:%s:%s", p, "_any_", "_any_")
    }
}

// ResolveConfig returns the first matching config and its key, in one round trip.
func (rl *RateLimiter) ResolveConfig(ctx context.Context, path, clientID, ip string) (*RateLimitConfig, string, error) {
    keys := candidateKeys(path, clientID, ip)

    pipe := rl.rdb.Pipeline()
    cmds := make([]*redis.MapStringStringCmd, len(keys))
    for i, k := range keys {
        cmds[i] = pipe.HGetAll(ctx, k)
    }
    if _, err := pipe.Exec(ctx); err != nil && err != redis.Nil {
        return nil, "", err
    }

    for i, cmd := range cmds {
        if m, _ := cmd.Result(); len(m) > 0 {
            return parseConfig(m), keys[i], nil
        }
    }
    return nil, "", redis.Nil // no rule
}
```

``` Rate Limit Check()
var tokenBucketScript = redis.NewScript(`
local key = KEYS[1]
local now = tonumber(ARGV[1])
local rate = tonumber(ARGV[2])
local burst = tonumber(ARGV[3])
local requested = tonumber(ARGV[4])

local data = redis.call('HMGET', key, 'tokens', 'last_refill')
local tokens = tonumber(data[1])
local last_refill = tonumber(data[2])

if tokens == nil then tokens = burst; last_refill = now end

local elapsed = math.max(0, now - last_refill)
tokens = math.min(burst, tokens + elapsed * rate / 1000.0)

local allowed, remaining, retry_after = 0, tokens, 0
if tokens >= requested then
    tokens = tokens - requested
    allowed, remaining = 1, tokens
else
    retry_after = math.ceil((requested - tokens) / rate)
    if retry_after < 1 then retry_after = 1 end
end

redis.call('HMSET', key, 'tokens', tokens, 'last_refill', now)
redis.call('EXPIRE', key, math.ceil(burst / rate) + 1)

return {allowed, math.floor(remaining), retry_after}
`)

// Check resolves the config by precedence, then enforces it.
// Returns (allowed, remaining, retryAfterSeconds, matchedKey, err).
func (rl *RateLimiter) Check(ctx context.Context, path, clientID, ip string) (bool, int, int, string, error) {
    cfg, matchedKey, err := rl.ResolveConfig(ctx, path, clientID, ip)
    if err == redis.Nil {
        return true, 0, 0, "", nil // no rule → allow by default
    }
    if err != nil {
        return false, 0, 0, "", err
    }

    res, err := tokenBucketScript.Run(ctx, rl.rdb,
        []string{counterKey(matchedKey)},
        time.Now().UnixMilli(), cfg.Rate, cfg.Burst, 1,
    ).Slice()
    if err != nil {
        return false, 0, 0, matchedKey, err
    }

    allowed := res[0].(int64) == 1
    remaining := int(res[1].(int64))
    retryAfter := int(res[2].(int64))
    return allowed, remaining, retryAfter, matchedKey, nil
}
```

7. Optimize and move the code logic to the proper folder and file.
a) move the rate limit config data model definition in the file `internal/service/rate-limiter.go` to the file `internal/repo/model/rate-limit-config.go`
b) move the rate limit config database operation in the file `internal/service/rate-limiter.go` to the file `internal/repo/orm/rate-limit.go`

8. what is the difference between CounterKey and ConfigKey?
a) why use CounterKey not ConfigKey in the method `func (rl *RateLimiter) takeToken()` located at `internal/middleware/rate-limiter.go` 
b) why use CounterKey in the method `func (s *Service) drop(ctx context.Context, key string) error` located at `internal/service/rate-limiter.go` 
c) where set the CouterKey into the redis hash
I am confused. Help me  analysize and explain the reason, without my permission, you can not modify the code?

can I think that CounterKey is needed for temporary storage for hash field settings?

9. add restful rate limit configure API about create, update, delete, get, list into the file `test/rest.http`
a) endpoint address: 127.0.0.1:18099
b) API definition referred to `registerRateLimitConfigRoutes` located at the file `internal/router/route.go`

10. Add rate limit configuare CURD operation for the `/api/v1/rate-limiter-mock` API in the file  `test/rest.http`

11. Add the Rate Limit Config of the Rate Limiter Mock API with the path `/api/v1/rate-limiter-mock` in the file `test/rest.http`, supporting the following scenarios
a) without client_id and ip, it has been added.
b) with client_id `client_001`, without ip
c) with client_id `client_001`, with ip `127.0.0.1`
d) without client_id, with ip `127.0.0.1`

12. Add a comparison in table format to show the advantages and disadvantages between Token Bucket and Fixed window strategy for rate limit plugin into the section `### Token Bucket vs Fixed window` in the `README.md`. Note: Keep concise and straight. Explain why we use Token Bucket strategy shortly. 

13. Add http client request in the file `test\python_req.py` written in python. Follow the rules as follows
a) Target API: send request to /api/v1/rate-limiter-mock with Get method
b) Provide CLI arguments: --rate=10(send the number of request per second), --host=127.0.0.1:18099(specify the target host),--count=20(total number of the request to send)
header parameter: --client_id=client_id_001(default value)
c) Retry: If the response has the header `Retry-After`, then wait for N seconds and try to send the same request again
d) Log: Print the result of each request, output format is like "[%d] [OK]", green color for ok, "%d" is the times of the request, when the response is normal. Output "[%d] [FAIL] [Try After %ds]", red color for fail and reply the lastest "%d" with the `Retry-After` value when the response has the `Retry-After`.
e) If the response has the header `Retry-After`, the client will sleep for `Retry-After`+1 seconds
f) 4 cases to mock the scenario
f.1) only path: no specified request parameter
f.2) with client_id, with ip: client_id->client_id_001, ip: 127.0.0.1
f.3) with client_id, without ip: client_id->client_id_001
f.4) without client_id, with ip: ip: 127.0.0.1
====================================TODO ==========================
14. The path in wildcarn partern is a little complex to constrain the rate limit.
 how to support wildcard path defined as regular expression rate limit configure?The complete uri path has been implemented in the file `internal/middleware/rate-limiter.go`. It looks like as follows:
a) /api/v1/order/(\+d)
b) /api/v1/config/(\+d)/fix
c) /api/v1/pay-service/[a-zA-Z0-9_-]+/detail
Analysis and give some suggestions, do not write code.

15. Add thread id print log (e.g [Thread %d] [%d] [OK]|[FAIL] [Try After %d] ) when sending http request in the file `test/python_req.py`