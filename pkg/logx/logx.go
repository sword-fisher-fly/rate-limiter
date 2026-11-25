package logx

import (
	"fmt"

	"gitee.com/fisher_devops_cicd/rate-limiter-middleware/conf"
	"github.com/pkg/errors"
	"github.com/toolkits/pkg/logger"
)

func Init(c conf.LogConfig) (func(), error) {
	logger.SetSeverity(c.Level)

	if c.Output == "stderr" {
		logger.LogToStderr()
	} else if c.Output == "file" {
		lb, err := logger.NewFileBackend(c.Dir)
		if err != nil {
			return nil, errors.WithMessage(err, "NewFileBackend failed")
		}

		if c.KeepHours != 0 {
			lb.SetRotateByHour(true)
			lb.SetKeepHours(c.KeepHours)
		} else if c.RotateNum != 0 {
			lb.Rotate(c.RotateNum, c.RotateSize*1024*1024)
		} else {
			return nil, errors.New("Both KeepHours and Rotatenum are null values")
		}
		lb.OutputToOneFile(c.OutputToOneFile)

		logger.SetLogging(c.Level, lb)
	}

	return func() {
		fmt.Println("logger exiting")
		logger.Close()
	}, nil
}
