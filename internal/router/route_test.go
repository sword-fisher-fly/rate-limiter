package router

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"gitee.com/fisher_devops_cicd/rate-limiter-middleware/conf"
	"gitee.com/fisher_devops_cicd/rate-limiter-middleware/pkg/ctx"
	"gitee.com/fisher_devops_cicd/rate-limiter-middleware/pkg/db"
	miniredis "github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

// newTestEngine returns a gin engine that serves the APIs of the service on
// top of a database that is created by the migration files and an in memory
// redis server.
func newTestEngine(t *testing.T) (*gin.Engine, *redis.Client) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	// The migrations are applied relative to the working directory of the
	// process, so the test runs in the root of the repository.
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get the working directory: %v", err)
	}
	if err := os.Chdir(filepath.Join("..", "..")); err != nil {
		t.Fatalf("failed to change the working directory: %v", err)
	}
	defer func() {
		if err := os.Chdir(wd); err != nil {
			t.Fatalf("failed to change the working directory: %v", err)
		}
	}()

	db, err := db.New(conf.DBConfig{
		DBType: "sqlite",
		DSN:    filepath.Join(t.TempDir(), "rate-limiter.db"),
	})
	if err != nil {
		t.Fatalf("failed to open the database: %v", err)
	}

	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})

	rt := New(ctx.NewContext(ctx.WithRedis(client), ctx.WithDB(db)))

	r := gin.New()
	rt.InstallAPIs(r)

	return r, client
}

// do sends the given request to the engine and returns the status code and
// the body of the response.
func do(t *testing.T, r *gin.Engine, method, path, body string) (int, map[string]interface{}) {
	t.Helper()

	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	var payload map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatalf("the response of %s %s is not a json object: %v", method, path, err)
	}

	return w.Code, payload
}

func TestRateLimitConfigAPI(t *testing.T) {
	r, client := newTestEngine(t)

	// The path of a config is required.
	status, payload := do(t, r, http.MethodPost, "/api/v1/rate-limit/configs", `{"name":"order pay","client_id":"client_001","rate":10,"burst":100}`)
	if status != http.StatusBadRequest {
		t.Fatalf("the status of the create without a path is %d, want %d", status, http.StatusBadRequest)
	}
	if payload["code"].(float64) != http.StatusBadRequest {
		t.Fatalf("the code of the response is %v", payload["code"])
	}

	// A rate that is not a number is rejected as well.
	status, _ = do(t, r, http.MethodPost, "/api/v1/rate-limit/configs", `{"name":"order pay","path":"/api/v1/order/pay","rate":0,"burst":100}`)
	if status != http.StatusBadRequest {
		t.Fatalf("the status of the create without a rate is %d, want %d", status, http.StatusBadRequest)
	}

	status, payload = do(t, r, http.MethodPost, "/api/v1/rate-limit/configs", `{"name":"order pay","path":"/api/v1/order/pay","client_id":"client_001","ip":"192.168.11.2","rate":10.5,"burst":100}`)
	if status != http.StatusOK {
		t.Fatalf("the status of the create is %d, want %d: %v", status, http.StatusOK, payload)
	}

	data := payload["data"].(map[string]interface{})
	id := int(data["id"].(float64))
	if data["path"] != "/api/v1/order/pay" || data["client_id"] != "client_001" || data["ip"] != "192.168.11.2" {
		t.Fatalf("the created config is %v", data)
	}
	if data["rate"].(float64) != 10.5 || data["burst"].(float64) != 100 {
		t.Fatalf("the rule of the created config is %v", data)
	}

	// The rule of the config is published in redis.
	fields, err := client.HGetAll(t.Context(), "rl:cfg:/api/v1/order/pay:client_001:192.168.11.2").Result()
	if err != nil {
		t.Fatalf("failed to read the rule: %v", err)
	}
	if fields["rate"] != "10.5" || fields["burst"] != "100" {
		t.Fatalf("the rule in redis is %v", fields)
	}

	// The config is read back.
	status, payload = do(t, r, http.MethodGet, "/api/v1/rate-limit/configs/1", "")
	if status != http.StatusOK {
		t.Fatalf("the status of the get is %d, want %d", status, http.StatusOK)
	}
	data = payload["data"].(map[string]interface{})
	if data["path"] != "/api/v1/order/pay" {
		t.Fatalf("the config is %v", data)
	}

	// A config that does not exist is not found.
	status, _ = do(t, r, http.MethodGet, "/api/v1/rate-limit/configs/404", "")
	if status != http.StatusNotFound {
		t.Fatalf("the status of the get of an unknown config is %d, want %d", status, http.StatusNotFound)
	}

	// The configs are listed.
	status, payload = do(t, r, http.MethodGet, "/api/v1/rate-limit/configs?page=1&page_size=10", "")
	if status != http.StatusOK {
		t.Fatalf("the status of the list is %d, want %d", status, http.StatusOK)
	}
	data = payload["data"].(map[string]interface{})
	if data["total"].(float64) != 1 || len(data["items"].([]interface{})) != 1 {
		t.Fatalf("the list of the configs is %v", data)
	}

	// The config is updated.
	status, _ = do(t, r, http.MethodPut, "/api/v1/rate-limit/configs/"+strconv.Itoa(id), `{"name":"order pay","path":"/api/v1/order/pay","client_id":"client_001","ip":"192.168.11.2","rate":20,"burst":50}`)
	if status != http.StatusOK {
		t.Fatalf("the status of the update is %d, want %d", status, http.StatusOK)
	}
	fields, err = client.HGetAll(t.Context(), "rl:cfg:/api/v1/order/pay:client_001:192.168.11.2").Result()
	if err != nil {
		t.Fatalf("failed to read the rule: %v", err)
	}
	if fields["rate"] != "20" || fields["burst"] != "50" {
		t.Fatalf("the rule in redis is %v", fields)
	}

	// The path of a config can not be edited.
	status, _ = do(t, r, http.MethodPut, "/api/v1/rate-limit/configs/"+strconv.Itoa(id), `{"name":"order refund","path":"/api/v1/order/refund","rate":20,"burst":50}`)
	if status != http.StatusBadRequest {
		t.Fatalf("the status of the update of the path is %d, want %d", status, http.StatusBadRequest)
	}

	// The config is deleted.
	status, _ = do(t, r, http.MethodDelete, "/api/v1/rate-limit/configs/"+strconv.Itoa(id), "")
	if status != http.StatusOK {
		t.Fatalf("the status of the delete is %d, want %d", status, http.StatusOK)
	}
	fields, err = client.HGetAll(t.Context(), "rl:cfg:/api/v1/order/pay:client_001:192.168.11.2").Result()
	if err != nil {
		t.Fatalf("failed to read the rule: %v", err)
	}
	if len(fields) != 0 {
		t.Fatalf("the rule is still in redis: %v", fields)
	}
}

func TestBusinessAPIIsRateLimited(t *testing.T) {
	r, _ := newTestEngine(t)

	// A rule that limits the requests of the mock API.
	status, _ := do(t, r, http.MethodPost, "/api/v1/rate-limit/configs", `{"name":"mock","path":"/api/v1/rate-limiter-mock","rate":1,"burst":1}`)
	if status != http.StatusOK {
		t.Fatalf("the status of the create is %d, want %d", status, http.StatusOK)
	}

	status, _ = do(t, r, http.MethodGet, "/api/v1/rate-limiter-mock", "")
	if status != http.StatusOK {
		t.Fatalf("the status of the first request is %d, want %d", status, http.StatusOK)
	}

	status, _ = do(t, r, http.MethodGet, "/api/v1/rate-limiter-mock", "")
	if status != http.StatusTooManyRequests {
		t.Fatalf("the status of the second request is %d, want %d", status, http.StatusTooManyRequests)
	}

	// The config APIs are not rate limited themselves.
	status, _ = do(t, r, http.MethodGet, "/api/v1/rate-limit/configs?page=1&page_size=10", "")
	if status != http.StatusOK {
		t.Fatalf("the status of the list is %d, want %d", status, http.StatusOK)
	}
}
