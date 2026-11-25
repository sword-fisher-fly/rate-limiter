package service

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"gitee.com/fisher_devops_cicd/rate-limiter-middleware/internal/repo/model"
	"gitee.com/fisher_devops_cicd/rate-limiter-middleware/internal/repo/orm"
	"gitee.com/fisher_devops_cicd/rate-limiter-middleware/pkg/redis"
	"github.com/toolkits/pkg/logger"
	"gorm.io/gorm"
)

const (
	// ConfigKeyPrefix is the prefix of the redis hash that holds the rule of
	// a rate limit config.
	ConfigKeyPrefix = "rl:cfg:"
	// CounterKeyPrefix is the prefix of the redis hash that holds the state
	// of the token bucket of a rate limit config.
	CounterKeyPrefix = "rl:tb:"
	// ConfigIndexKey is the redis set that holds the key of every rate limit
	// config, it is used to enumerate the rules without scanning the
	// keyspace of the redis server.
	ConfigIndexKey = "rl:cfg:index"
	// AnyValue is the placeholder of a client id or an ip that is not set in
	// the key of a rate limit config.
	AnyValue = "_any_"

	// RuleFieldRate and RuleFieldBurst are the fields of the redis hash of a
	// rate limit config that hold the rule.
	RuleFieldRate  = "rate"
	RuleFieldBurst = "burst"
)

var (
	ErrNotFound      = orm.ErrNotFound
	ErrPathImmutable = errors.New("the path of the rate limit config can not be edited")
	ErrDuplicate     = errors.New("the rate limit config exists already")
)

type Service struct {
	store *orm.RateLimitConfigStore
	rdb   redis.Redis
}

func New(db *gorm.DB, rdb redis.Redis) *Service {
	return &Service{
		store: orm.NewRateLimitConfigStore(db),
		rdb:   rdb,
	}
}

// NormalizeValue returns the form of a client id or an ip used in the key of
// a rate limit config. The values are case insensitive, an unset value
// becomes the AnyValue placeholder.
func NormalizeValue(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return AnyValue
	}
	return value
}

// NormalizePath returns the form of a request path used in the key of a rate
// limit config. A path is case sensitive, so it is only trimmed.
func NormalizePath(path string) string {
	return strings.TrimSpace(path)
}

// ConfigKey returns the key of the redis hash that holds the rule of the
// rate limit config of the given path, client id and ip.
func ConfigKey(path, clientID, ip string) string {
	return ConfigKeyPrefix + NormalizePath(path) + ":" + NormalizeValue(clientID) + ":" + NormalizeValue(ip)
}

// CounterKey returns the key of the redis hash that holds the state of the
// token bucket of the given rate limit config.
func CounterKey(configKey string) string {
	return CounterKeyPrefix + strings.TrimPrefix(configKey, ConfigKeyPrefix)
}

// Create stores the given rate limit config in the database and publishes
// its rule in redis.
func (s *Service) Create(ctx context.Context, cfg *model.RateLimitConfig) error {
	duplicate, err := s.duplicate(ctx, cfg.Path, cfg.ClientID, cfg.IP, 0)
	if err != nil {
		return err
	}
	if duplicate {
		return ErrDuplicate
	}

	if err := s.store.Create(ctx, cfg); err != nil {
		return err
	}

	key := ConfigKey(cfg.Path, cfg.ClientID, cfg.IP)
	if err := s.publish(ctx, key, cfg.Rate, cfg.Burst); err != nil {
		// A config whose rule is missing in redis is not enforced, so the
		// record is removed again to keep both stores in sync.
		if delErr := s.store.Delete(ctx, cfg.ID); delErr != nil {
			logger.Errorf("failed to remove the rate limit config %d from the database: %v", cfg.ID, delErr)
		}
		if delErr := s.drop(ctx, key); delErr != nil {
			logger.Errorf("failed to remove the rate limit config %s from redis: %v", key, delErr)
		}
		return err
	}

	return nil
}

// Update changes the given rate limit config in the database and publishes
// its rule in redis.
func (s *Service) Update(ctx context.Context, id int64, cfg *model.RateLimitConfig) error {
	current, err := s.Get(ctx, id)
	if err != nil {
		return err
	}

	if NormalizePath(current.Path) != NormalizePath(cfg.Path) {
		return ErrPathImmutable
	}

	duplicate, err := s.duplicate(ctx, cfg.Path, cfg.ClientID, cfg.IP, id)
	if err != nil {
		return err
	}
	if duplicate {
		return ErrDuplicate
	}

	cfg.ID = id
	if err := s.store.Update(ctx, id, cfg); err != nil {
		return err
	}

	oldKey := ConfigKey(current.Path, current.ClientID, current.IP)
	newKey := ConfigKey(cfg.Path, cfg.ClientID, cfg.IP)

	if oldKey != newKey {
		// key has changed, so drop the old key in redis.
		if err := s.drop(ctx, oldKey); err != nil {
			return err
		}
	}

	if err := s.publish(ctx, newKey, cfg.Rate, cfg.Burst); err != nil {
		if restoreErr := s.restore(ctx, current, oldKey, newKey); restoreErr != nil {
			logger.Errorf("failed to restore the rate limit config %d: %v", id, restoreErr)
		}
		return err
	}

	return nil
}

func (s *Service) Get(ctx context.Context, id int64) (*model.RateLimitConfig, error) {
	return s.store.Get(ctx, id)
}

func (s *Service) List(ctx context.Context, page, pageSize int) ([]model.RateLimitConfig, int64, error) {
	return s.store.List(ctx, page, pageSize)
}

func (s *Service) Delete(ctx context.Context, id int64) error {
	cfg, err := s.Get(ctx, id)
	if err != nil {
		return err
	}

	if err := s.store.Delete(ctx, id); err != nil {
		return err
	}

	if err := s.drop(ctx, ConfigKey(cfg.Path, cfg.ClientID, cfg.IP)); err != nil {
		return err
	}

	return nil
}

// duplicate reports whether a rate limit config with the same path, client
// id and ip exists already.
func (s *Service) duplicate(ctx context.Context, path, clientID, ip string, id int64) (bool, error) {
	configs, err := s.store.FindByPath(ctx, path)
	if err != nil {
		return false, err
	}

	for _, cfg := range configs {
		if cfg.ID == id {
			continue
		}
		if NormalizePath(cfg.Path) != NormalizePath(path) {
			continue
		}
		if NormalizeValue(cfg.ClientID) != NormalizeValue(clientID) {
			continue
		}
		if NormalizeValue(cfg.IP) != NormalizeValue(ip) {
			continue
		}
		return true, nil
	}

	return false, nil
}

// publish writes the rule of a rate limit config into its redis hash and
// adds the key of the hash to the index of the configs.
func (s *Service) publish(ctx context.Context, key string, rate float64, burst int) error {
	pipe := s.rdb.TxPipeline()
	pipe.HSet(ctx, key, RuleFieldRate, strconv.FormatFloat(rate, 'f', -1, 64), RuleFieldBurst, burst)
	pipe.SAdd(ctx, ConfigIndexKey, key)

	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("failed to publish the rate limit config %s in redis: %v", key, err)
	}

	return nil
}

// drop removes the rule of a rate limit config from redis, together with the
// state of its token bucket and its entry in the index of the configs.
func (s *Service) drop(ctx context.Context, key string) error {
	pipe := s.rdb.TxPipeline()
	pipe.Del(ctx, key)
	pipe.Del(ctx, CounterKey(key))
	pipe.SRem(ctx, ConfigIndexKey, key)

	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("failed to drop the rate limit config %s from redis: %v", key, err)
	}

	return nil
}

// restore writes the given rate limit config back into the database and
// publishes its rule in redis again, it is used when the publish of an
// update failed.
func (s *Service) restore(ctx context.Context, cfg *model.RateLimitConfig, oldKey, newKey string) error {
	if err := s.store.Update(ctx, cfg.ID, cfg); err != nil {
		return err
	}

	if oldKey != newKey {
		if err := s.drop(ctx, newKey); err != nil {
			return err
		}
	}

	return s.publish(ctx, oldKey, cfg.Rate, cfg.Burst)
}
