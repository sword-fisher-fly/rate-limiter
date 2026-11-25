package model

// RateLimitConfig is a rate limit rule of the service, it is stored in the
// rate_limit_config table and published in redis.
type RateLimitConfig struct {
	ID       int64   `json:"id" gorm:"column:id;primaryKey;autoIncrement"`
	Name     string  `json:"name" gorm:"column:name"`
	Path     string  `json:"path" gorm:"column:request_path"`
	ClientID string  `json:"client_id" gorm:"column:client_id"`
	IP       string  `json:"ip" gorm:"column:ip"`
	Rate     float64 `json:"rate" gorm:"column:rate"`
	Burst    int     `json:"burst" gorm:"column:burst"`
}
