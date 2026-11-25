CREATE TABLE IF NOT EXISTS `rate_limit_config` (
    `id` bigint NOT NULL AUTO_INCREMENT COMMENT 'primary key',
    `name` varchar(200) NOT NULL COMMENT 'rate limiter name',
    `request_path` varchar(200) NOT NULL COMMENT 'client request uri path',
    `client_id` varchar(50) NOT NULL COMMENT 'client ID from client request header',
    `ip` varchar(50) NOT NULL COMMENT 'client ip',
    `rate` double NOT NULL COMMENT 'number of the specified request per second',
    `burst` int NOT NULL DEFAULT 0 COMMENT 'max number of the request allowed at startup',
    PRIMARY KEY (`id`),
    KEY `idx_name` (`name`),
    KEY `idx_request_path` (`request_path`),
    KEY `idx_client_id` (`client_id`),
    KEY `idx_ip` (`ip`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci COMMENT='Rate Limiter Configure Table';