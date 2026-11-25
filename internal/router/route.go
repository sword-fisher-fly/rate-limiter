package router

import (
	"strings"

	"gitee.com/fisher_devops_cicd/rate-limiter-middleware/conf"
	"gitee.com/fisher_devops_cicd/rate-limiter-middleware/internal/api"
	"gitee.com/fisher_devops_cicd/rate-limiter-middleware/internal/middleware"
	"gitee.com/fisher_devops_cicd/rate-limiter-middleware/pkg/aop"
	"gitee.com/fisher_devops_cicd/rate-limiter-middleware/pkg/ctx"
	"github.com/gin-gonic/gin"
)

type Router struct {
	HTTP conf.HTTPConfig
	Ctx  *ctx.Context
}

const (
	APIPrefix = "/api/v1"
)

func New(c *ctx.Context) *Router {
	return &Router{
		Ctx:  c,
		HTTP: conf.GetHTTPConfig(),
	}
}

func hasPathPrefix(path, prefix string) bool {
	return path == prefix || strings.HasPrefix(path, prefix+"/")
}

func isAPIPath(path string) bool {
	return hasPathPrefix(path, APIPrefix)
}

func (rt *Router) InstallAPIs(r *gin.Engine) {
	r.Use(aop.Recovery())

	// injectAppCtx makes shared dependencies available to every handler via
	// c.MustGet(ctx.GinKey), avoiding per-route closures.
	r.Use(rt.injectAppCtx())

	rt.registerAPIRoutes(r.Group(APIPrefix))

	// TODO: embeded web assets and static file
	// r.StaticFS("/assets", static.GetAssetsFS())

	// // Everything else that lands in dist/ — `public/` assets copied verbatim
	// // by Vite, the favicon, … — is served from the dist root under /static.
	// r.StaticFS("/static", static.GetRootFS())

	// r.NoRoute(func(c *gin.Context) {
	// 	if isAPIPath(c.Request.URL.Path) {
	// 		c.JSON(404, gin.H{
	// 			"code": 404,
	// 			"msg":  "api not found",
	// 		})
	// 		return
	// 	}

	// 	data, err := static.ReadFile("index.html")
	// 	if err != nil {
	// 		c.String(500, "index.html not found")
	// 		return
	// 	}

	// 	html := string(data)

	// 	c.Header("Cache-Control", "no-cache, no-store, must-revalidate")
	// 	c.Data(200, "text/html; charset=utf-8", []byte(html))
	// })
}


func (rt *Router) registerAPIRoutes(base *gin.RouterGroup) {
	// The rate limit config APIs are not rate limited themselves.
	rt.registerRateLimitConfigRoutes(base.Group("/rate-limit"))

	// Other business API is limited by the rules of the rate limit configs.
	business := base.Group("")
	business.Use(middleware.RateLimit(middleware.NewRateLimiter(rt.Ctx.Redis())))
	business.GET("/rate-limiter-mock", api.RateLimiterMock)
}

func (rt *Router) registerRateLimitConfigRoutes(group *gin.RouterGroup) {
	group.POST("/configs", api.CreateRateLimitConfig)
	group.GET("/configs", api.ListRateLimitConfigs)
	group.GET("/configs/:id", api.GetRateLimitConfig)
	group.PUT("/configs/:id", api.UpdateRateLimitConfig)
	group.DELETE("/configs/:id", api.DeleteRateLimitConfig)
}

func (rt *Router) injectAppCtx() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Set(ctx.GinKey, rt.Ctx)
		c.Next()
	}
}
