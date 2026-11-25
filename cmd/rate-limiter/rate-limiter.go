package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"gitee.com/fisher_devops_cicd/rate-limiter-middleware/conf"
	"gitee.com/fisher_devops_cicd/rate-limiter-middleware/internal/router"
	"gitee.com/fisher_devops_cicd/rate-limiter-middleware/pkg/ctx"
	"gitee.com/fisher_devops_cicd/rate-limiter-middleware/pkg/db"
	"gitee.com/fisher_devops_cicd/rate-limiter-middleware/pkg/httpx"
	"gitee.com/fisher_devops_cicd/rate-limiter-middleware/pkg/logx"
	"gitee.com/fisher_devops_cicd/rate-limiter-middleware/pkg/redis"
)

var Version = "v1.0.0"

var (
	showVersion = flag.Bool("version", false, "show version")
)

func Initialize() (func(), error) {
	// Load config
	if err := conf.InitConfig(); err != nil {
		return nil, err
	}

	// Create logger instance
	logxClean, err := logx.Init(conf.GetLogConfig())
	if err != nil {
		return nil, fmt.Errorf("failed to init logx: %v", err)
	}

	// Create Redis Client instance
	redis, err := redis.New(conf.GetRedisConfig())
	if err != nil {
		return nil, fmt.Errorf("failed to init redis: %v", err)
	}

	// Create database instance and init the tables of the database
	database, err := db.New(conf.GetDBConfig())
	if err != nil {
		return nil, fmt.Errorf("failed to init database: %v", err)
	}

	appCtx := ctx.NewContext(ctx.WithRedis(redis), ctx.WithDB(database))

	// Create Gin Engine instance and install route APIs
	rt := router.New(appCtx)

	r := httpx.GinEngine(conf.GetGlobalConfig().RunMode, conf.GetHTTPConfig(), nil, func() bool { return true })
	rt.InstallAPIs(r)

	httpClean := httpx.Init(conf.GetHTTPConfig(), r)

	return func() {
		logxClean()
		httpClean()

		if sqlDB, err := database.DB(); err == nil {
			sqlDB.Close()
		}
	}, nil
}

func main() {
	flag.Parse()

	if *showVersion {
		fmt.Println(Version)
		return
	}

	cleanFunc, err := Initialize()
	if err != nil {
		log.Fatalln("failed to set up server: ", err)
	}

	s := make(chan os.Signal, 1)

	signal.Notify(s, syscall.SIGHUP, syscall.SIGQUIT, syscall.SIGTERM, syscall.SIGINT)

	for {
		si := <-s
		switch si {
		case syscall.SIGQUIT, syscall.SIGTERM, syscall.SIGINT:
			log.Println("receive a signal to exit.", si)
			cleanFunc()
			os.Exit(0)
		case syscall.SIGHUP:
		default:
			return
		}
	}
}
