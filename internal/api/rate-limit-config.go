package api

import (
	"errors"
	"net/http"
	"strconv"

	"gitee.com/fisher_devops_cicd/rate-limiter-middleware/internal/repo/model"
	"gitee.com/fisher_devops_cicd/rate-limiter-middleware/internal/service"
	"gitee.com/fisher_devops_cicd/rate-limiter-middleware/pkg/ctx"
	"github.com/gin-gonic/gin"
)

type RateLimitConfigForm struct {
	Name     string  `json:"name" binding:"required,max=200"`
	Path     string  `json:"path" binding:"required,max=200"`
	ClientID string  `json:"client_id" binding:"omitempty,max=50"`
	IP       string  `json:"ip" binding:"omitempty,max=50"`
	Rate     float64 `json:"rate" binding:"required,gt=0"`
	Burst    int     `json:"burst" binding:"required,gte=1"`
}

type ListRateLimitConfigsData struct {
	Items []model.RateLimitConfig `json:"items"`
	Total int64                   `json:"total"`
}

// CreateRateLimitConfig creates a rate limit config and publishes its rule
// in redis.
func CreateRateLimitConfig(c *gin.Context) {
	var form RateLimitConfigForm
	if err := c.ShouldBindJSON(&form); err != nil {
		respondError(c, http.StatusBadRequest, err.Error())
		return
	}

	cfg := &model.RateLimitConfig{
		Name:     form.Name,
		Path:     form.Path,
		ClientID: form.ClientID,
		IP:       form.IP,
		Rate:     form.Rate,
		Burst:    form.Burst,
	}

	if err := rateLimitConfigService(c).Create(c.Request.Context(), cfg); err != nil {
		if errors.Is(err, service.ErrDuplicate) {
			respondError(c, http.StatusConflict, err.Error())
			return
		}
		respondError(c, http.StatusInternalServerError, err.Error())
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"msg":  "ok",
		"data": cfg,
	})
}

// GetRateLimitConfig returns the rate limit config with the id of the
// request path.
func GetRateLimitConfig(c *gin.Context) {
	id, ok := rateLimitConfigID(c)
	if !ok {
		return
	}

	cfg, err := rateLimitConfigService(c).Get(c.Request.Context(), id)
	if err != nil {
		if errors.Is(err, service.ErrNotFound) {
			respondError(c, http.StatusNotFound, err.Error())
			return
		}
		respondError(c, http.StatusInternalServerError, err.Error())
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"msg":  "ok",
		"data": cfg,
	})
}

// ListRateLimitConfigs returns the rate limit configs of the requested page.
func ListRateLimitConfigs(c *gin.Context) {
	page, err := queryInt(c, "page", 0)
	if err != nil {
		respondError(c, http.StatusBadRequest, err.Error())
		return
	}

	pageSize, err := queryInt(c, "page_size", 0)
	if err != nil {
		respondError(c, http.StatusBadRequest, err.Error())
		return
	}

	configs, total, err := rateLimitConfigService(c).List(c.Request.Context(), page, pageSize)
	if err != nil {
		respondError(c, http.StatusInternalServerError, err.Error())
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"msg":  "ok",
		"data": ListRateLimitConfigsData{
			Items: configs,
			Total: total,
		},
	})
}

// UpdateRateLimitConfig changes the rate limit config with the id of the
// request path and publishes its rule in redis.
func UpdateRateLimitConfig(c *gin.Context) {
	id, ok := rateLimitConfigID(c)
	if !ok {
		return
	}

	var form RateLimitConfigForm
	if err := c.ShouldBindJSON(&form); err != nil {
		respondError(c, http.StatusBadRequest, err.Error())
		return
	}

	cfg := &model.RateLimitConfig{
		Name:     form.Name,
		Path:     form.Path,
		ClientID: form.ClientID,
		IP:       form.IP,
		Rate:     form.Rate,
		Burst:    form.Burst,
	}

	if err := rateLimitConfigService(c).Update(c.Request.Context(), id, cfg); err != nil {
		if errors.Is(err, service.ErrPathImmutable) || errors.Is(err, service.ErrNotFound) {
			respondError(c, http.StatusBadRequest, err.Error())
			return
		}
		if errors.Is(err, service.ErrDuplicate) {
			respondError(c, http.StatusConflict, err.Error())
			return
		}
		respondError(c, http.StatusInternalServerError, err.Error())
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"msg":  "ok",
	})
}

// DeleteRateLimitConfig removes the rate limit config with the id of the
// request path and drops its rule from redis.
func DeleteRateLimitConfig(c *gin.Context) {
	id, ok := rateLimitConfigID(c)
	if !ok {
		return
	}

	if err := rateLimitConfigService(c).Delete(c.Request.Context(), id); err != nil {
		if errors.Is(err, service.ErrNotFound) {
			respondError(c, http.StatusNotFound, err.Error())
			return
		}
		respondError(c, http.StatusInternalServerError, err.Error())
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"msg":  "ok",
	})
}

func rateLimitConfigService(c *gin.Context) *service.Service {
	appCtx := c.MustGet(ctx.GinKey).(*ctx.Context)
	return service.New(appCtx.DB(), appCtx.Redis())
}

func rateLimitConfigID(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		respondError(c, http.StatusBadRequest, "the id of the rate limit config is invalid")
		return 0, false
	}

	return id, true
}

// queryInt returns the number of the given query parameter of the request,
// the fallback is used when the parameter is not set.
func queryInt(c *gin.Context, name string, fallback int) (int, error) {
	value := c.Query(name)
	if value == "" {
		return fallback, nil
	}

	number, err := strconv.Atoi(value)
	if err != nil {
		return 0, errors.New("the query parameter " + name + " is not a number")
	}

	return number, nil
}

func respondError(c *gin.Context, status int, msg string) {
	c.JSON(status, gin.H{
		"code": status,
		"msg":  msg,
	})
}
