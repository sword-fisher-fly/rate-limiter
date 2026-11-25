package api

import (
	"fmt"
	"net/http"
	"time"

	"gitee.com/fisher_devops_cicd/rate-limiter-middleware/pkg/ctx"
	"gitee.com/fisher_devops_cicd/rate-limiter-middleware/pkg/redis"
	"github.com/gin-gonic/gin"
)

const (
	mockRequestTotalKey  = "rl:mock:request_total"
	mockRequestWindowKey = "rl:mock:request_window"
	mockRateWindow       = time.Second
)

type RateLimiterMockData struct {
	RequestTotal  int64 `json:"request_total"`
	RatePerSecond int64 `json:"rate_per_second"`
}

// RateLimiterMock counts the requests that went through the rate limiter
// and reports how many of them arrived during the last second.
func RateLimiterMock(c *gin.Context) {
	appCtx := c.MustGet(ctx.GinKey).(*ctx.Context)
	r := appCtx.Redis()

	total, err := r.Incr(appCtx.Context(), mockRequestTotalKey).Result()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"code": 500,
			"msg":  fmt.Sprintf("failed to count the request: %v", err),
		})
		return
	}

	rate, err := redis.IncrWindow(appCtx.Context(), r, mockRequestWindowKey, mockRateWindow)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"code": 500,
			"msg":  fmt.Sprintf("failed to count the request rate: %v", err),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"msg":  "ok",
		"data": RateLimiterMockData{
			RequestTotal:  total,
			RatePerSecond: rate,
		},
	})
}
