package conf

import (
	"flag"
	"fmt"

	"gitee.com/fisher_devops_cicd/rate-limiter-middleware/pkg/util"
	"github.com/spf13/viper"
)

type RedisConfig struct {
	Address          string `yaml:"address" mapstructure:"address"`
	Username         string `yaml:"username" mapstructure:"username"`
	Password         string `yaml:"password" mapstructure:"password"`
	DB               int    `yaml:"db" mapstructure:"db"`
	RedisType        string `yaml:"type" mapstructure:"type"`
	MasterName       string `yaml:"master_name" mapstructure:"master_name"`
	SentinelUsername string `yaml:"sentinel_username" mapstructure:"sentinel_username"`
	SentinelPassword string `yaml:"sentinel_password" mapstructure:"sentinel_password"`
}

type DBConfig struct {
	Debug        bool   `yaml:"debug" mapstructure:"debug"`
	DBType       string `yaml:"type" mapstructure:"type"`
	DSN          string `yaml:"dsn" mapstructure:"dsn"`
	MaxLifetime  int    `yaml:"max_life_time" mapstructure:"max_life_time"`
	MaxOpenConns int    `yaml:"max_open_conns" mapstructure:"max_open_conns"`
	MaxIdleConns int    `yaml:"max_idle_conns" mapstructure:"max_idle_conns"`
	TablePrefix  string `yaml:"table_prefix" mapstructure:"table_prefix"`
}

type LogConfig struct {
	Dir             string `yaml:"dir" mapstructure:"dir"`
	Level           string `yaml:"level" mapstructure:"level"`
	Output          string `yaml:"output" mapstructure:"output"`
	KeepHours       uint   `yaml:"keep_hours" mapstructure:"keep_hours"`
	RotateNum       int    `yaml:"rotate_num" mapstructure:"rotate_num"`
	RotateSize      uint64 `yaml:"rotate_size" mapstructure:"rotate_size"`
	OutputToOneFile bool   `yaml:"output_to_one_file" mapstructure:"output_to_one_file"`
}

type HTTPConfig struct {
	Host             string `yaml:"host" mapstructure:"host"`
	Port             int    `yaml:"port" mapstructure:"port"`
	WebHost          string `yaml:"web_host" mapstructure:"web_host"`
	PProf            bool   `yaml:"pprof" mapstructure:"pprof"`
	PrintAccessLog   bool   `yaml:"print_access_log" mapstructure:"print_access_log"`
	PrintBody        bool   `yaml:"print_body" mapstructure:"print_body"`
	ExposeMetrics    bool   `yaml:"expose_metrics" mapstructure:"expose_metrics"`
	ShutdownTimeout  int    `yaml:"shutdown_timeout" mapstructure:"shutdown_timeout"`
	MaxContentLength int64  `yaml:"max_content_length" mapstructure:"max_content_length"`
	ReadTimeout      int    `yaml:"read_timeout" mapstructure:"read_timeout"`
	WriteTimeout     int    `yaml:"write_timeout" mapstructure:"write_timeout"`
	IdleTimeout      int    `yaml:"idle_timeout" mapstructure:"idle_timeout"`
}

type GlobalConfig struct {
	RunMode string `yaml:"run_mode" mapstructure:"run_mode"`
}

type ConfigType struct {
	Global GlobalConfig `yaml:"global" mapstructure:"global"`
	Log    LogConfig    `yaml:"log" mapstructure:"log"`
	HTTP   HTTPConfig   `yaml:"http" mapstructure:"http"`
	Redis  RedisConfig  `yaml:"redis" mapstructure:"redis"`
	DB     DBConfig     `yaml:"db" mapstructure:"db"`
}

var config ConfigType
var configDir = flag.String("config", "./etc/config.yaml", "config dir, refer to the config file defined in etc")

func InitConfig() error {
	exists, err := util.FileExists(*configDir)
	if err != nil {
		return fmt.Errorf("config file not found, err: %v", err)
	}

	// load env file for local test
	if exists {
		viper.SetConfigFile(*configDir)
		if err := viper.ReadInConfig(); err != nil {
			return fmt.Errorf("failed to read config file, err: %v", err)
		}
	}

	// Bind the settings a deployment has to override without editing the
	// config file — a container image, for instance, is handed the port it
	// must listen on. AutomaticEnv alone does not reach Unmarshal; only
	// explicitly bound keys are consulted.
	viper.BindEnv("http.port", "HTTP_PORT")

	if err := viper.Unmarshal(&config); err != nil {
		return fmt.Errorf("failed to unmarshal config: %v", err)
	}

	// load all Environment variables
	viper.AutomaticEnv()

	return nil
}

func GetGlobalConfig() GlobalConfig {
	return config.Global
}

func GetLogConfig() LogConfig {
	return config.Log
}

func GetHTTPConfig() HTTPConfig {
	return config.HTTP
}

func GetDBConfig() DBConfig {
	return config.DB
}

func GetRedisConfig() RedisConfig {
	return config.Redis
}
