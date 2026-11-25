package orm

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"gitee.com/fisher_devops_cicd/rate-limiter-middleware/internal/repo/model"
	"gorm.io/gorm"
)

const (
	// DefaultPageSize is the number of the configs the list query returns
	// when the request does not ask for a page size.
	DefaultPageSize = 10
	// MaxPageSize is the largest number of the configs the list query
	// returns.
	MaxPageSize = 100
)

// ErrNotFound is returned when a rate limit config does not exist.
var ErrNotFound = errors.New("the rate limit config does not exist")

// RateLimitConfigStore holds the database operations of the rate limit
// configs.
type RateLimitConfigStore struct {
	db *gorm.DB
}

// NewRateLimitConfigStore returns the store of the rate limit configs of the
// given database.
func NewRateLimitConfigStore(db *gorm.DB) *RateLimitConfigStore {
	return &RateLimitConfigStore{db: db}
}

// Create stores the given rate limit config in the database and fills its id.
func (s *RateLimitConfigStore) Create(ctx context.Context, cfg *model.RateLimitConfig) error {
	if err := s.db.WithContext(ctx).Create(cfg).Error; err != nil {
		return fmt.Errorf("failed to create the rate limit config: %v", err)
	}

	return nil
}

// Get returns the rate limit config with the given id.
func (s *RateLimitConfigStore) Get(ctx context.Context, id int64) (*model.RateLimitConfig, error) {
	var cfg model.RateLimitConfig
	if err := s.db.WithContext(ctx).Where("id = ?", id).First(&cfg).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("failed to get the rate limit config %d: %v", id, err)
	}

	return &cfg, nil
}

// List returns the rate limit configs of the given page and the total number
// of the stored configs. A page or a page size that is not a number asks for
// the first page and for the default page size.
func (s *RateLimitConfigStore) List(ctx context.Context, page, pageSize int) ([]model.RateLimitConfig, int64, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = DefaultPageSize
	}
	if pageSize > MaxPageSize {
		pageSize = MaxPageSize
	}

	var total int64
	if err := s.db.WithContext(ctx).Model(&model.RateLimitConfig{}).Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("failed to count the rate limit configs: %v", err)
	}

	var configs []model.RateLimitConfig
	if err := s.db.WithContext(ctx).
		Order("id desc").
		Offset((page - 1) * pageSize).
		Limit(pageSize).
		Find(&configs).Error; err != nil {
		return nil, 0, fmt.Errorf("failed to list the rate limit configs: %v", err)
	}

	return configs, total, nil
}

// Update changes the given rate limit config in the database. The path of a
// config is not part of the update, it can not be edited.
func (s *RateLimitConfigStore) Update(ctx context.Context, id int64, cfg *model.RateLimitConfig) error {
	update := model.RateLimitConfig{
		Name:     cfg.Name,
		ClientID: cfg.ClientID,
		IP:       cfg.IP,
		Rate:     cfg.Rate,
		Burst:    cfg.Burst,
	}

	// The fields are selected, so the ones that are empty are written too.
	if err := s.db.WithContext(ctx).
		Model(&model.RateLimitConfig{}).
		Where("id = ?", id).
		Select("name", "client_id", "ip", "rate", "burst").
		Updates(update).Error; err != nil {
		return fmt.Errorf("failed to update the rate limit config %d: %v", id, err)
	}

	return nil
}

// Delete removes the rate limit config with the given id from the database.
func (s *RateLimitConfigStore) Delete(ctx context.Context, id int64) error {
	if err := s.db.WithContext(ctx).Delete(&model.RateLimitConfig{}, id).Error; err != nil {
		return fmt.Errorf("failed to delete the rate limit config %d: %v", id, err)
	}

	return nil
}

// FindByPath returns the rate limit configs of the given path. The path of a
// config is stored as it was sent, so the trimmed path is looked up as well.
func (s *RateLimitConfigStore) FindByPath(ctx context.Context, path string) ([]model.RateLimitConfig, error) {
	var configs []model.RateLimitConfig
	if err := s.db.WithContext(ctx).
		Where("request_path IN ?", []string{path, strings.TrimSpace(path)}).
		Find(&configs).Error; err != nil {
		return nil, fmt.Errorf("failed to look up the rate limit configs of %s: %v", path, err)
	}

	return configs, nil
}
