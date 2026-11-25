package ctx

import (
	"context"

	"gitee.com/fisher_devops_cicd/rate-limiter-middleware/pkg/redis"
	"gorm.io/gorm"
)

// GinKey is the gin.Context key under which Router injects the shared *Context
// so handlers can pull dependencies via c.MustGet(ctx.GinKey).
const GinKey = "app_ctx"

type Context struct {
	redisClient redis.Redis
	db          *gorm.DB
	ctx         context.Context
}

type Option func(*Context)

func WithRedis(redisClient redis.Redis) Option {
	return func(c *Context) {
		c.redisClient = redisClient
	}
}

func WithDB(db *gorm.DB) Option {
	return func(c *Context) {
		c.db = db
	}
}

func WithContext(ctx context.Context) Option {
	return func(c *Context) {
		c.ctx = ctx
	}
}

func NewContext(opts ...Option) *Context {
	ctx := &Context{
		ctx: context.Background(),
	}

	for _, opt := range opts {
		opt(ctx)
	}

	return ctx
}

// Redis returns the underlying redis.Redis instance.
func (c *Context) Redis() redis.Redis {
	return c.redisClient
}

// DB returns the underlying *gorm.DB instance.
func (c *Context) DB() *gorm.DB {
	return c.db
}

func (c *Context) Context() context.Context {
	return c.ctx
}
