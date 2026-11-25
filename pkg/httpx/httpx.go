package httpx

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"gitee.com/fisher_devops_cicd/rate-limiter-middleware/conf"
	"gitee.com/fisher_devops_cicd/rate-limiter-middleware/pkg/aop"
	"github.com/gin-contrib/pprof"
	"github.com/gin-gonic/gin"
)

func GinEngine(
	mode string,
	cfg conf.HTTPConfig,
	printBodyPaths func() map[string]struct{},
	printAccessLog func() bool,
) *gin.Engine {
	gin.SetMode(mode)

	loggerMid := aop.Logger(
		aop.LoggerConfig{
			PrintAccessLog: printAccessLog,
			PrintBodyPaths: printBodyPaths,
		})
	recoveryMid := aop.Recovery()

	if strings.ToLower(mode) == "release" {
		aop.DisableConsoleColor()
	}

	r := gin.New()

	r.Use(recoveryMid)

	r.Use(loggerMid)

	if cfg.PProf {
		pprof.Register(r, "/rate-limiter/api/v1/debug/pprof")
	}

	return r
}

func Init(cfg conf.HTTPConfig, handler http.Handler) func() {
	addr := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)
	srv := &http.Server{
		Addr:         addr,
		Handler:      handler,
		ReadTimeout:  time.Duration(cfg.ReadTimeout) * time.Second,
		WriteTimeout: time.Duration(cfg.WriteTimeout) * time.Second,
		IdleTimeout:  time.Duration(cfg.IdleTimeout) * time.Second,
	}

	go func() {
		fmt.Println("http server listening on:", addr)

		var err error
		err = srv.ListenAndServe()

		if err != nil && err != http.ErrServerClosed {
			panic(err)
		}
	}()

	return func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second*time.Duration(cfg.ShutdownTimeout))
		defer cancel()

		srv.SetKeepAlivesEnabled(false)
		if err := srv.Shutdown(ctx); err != nil {
			fmt.Println("cannot shutdown http server:", err)
		}

		select {
		case <-ctx.Done():
			fmt.Println("http exiting")
		default:
			fmt.Println("http server stopped")
		}
	}
}
