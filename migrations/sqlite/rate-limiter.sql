-- SQLite flavour of migrations/sql/rate-limiter.sql, the schema of the tables
-- is the same. SQLite has no column comments and no inline indexes, so the
-- comments are kept as line comments and the indexes are created separately.

CREATE TABLE IF NOT EXISTS `rate_limit_config` (
    `id` INTEGER PRIMARY KEY AUTOINCREMENT, -- primary key
    `name` TEXT NOT NULL, -- rate limiter name
    `request_path` TEXT NOT NULL, -- client request uri path
    `client_id` TEXT NOT NULL, -- client ID from client request header
    `ip` TEXT NOT NULL, -- client ip
    `rate` DOUBLE PRECISION NOT NULL, -- number of the specified request per second
    `burst` INTEGER NOT NULL DEFAULT 0 -- max number of the request allowed at startup
);

CREATE INDEX IF NOT EXISTS `idx_name` ON `rate_limit_config` (`name`);

CREATE INDEX IF NOT EXISTS `idx_request_path` ON `rate_limit_config` (`request_path`);

CREATE INDEX IF NOT EXISTS `idx_client_id` ON `rate_limit_config` (`client_id`);

CREATE INDEX IF NOT EXISTS `idx_ip` ON `rate_limit_config` (`ip`);
