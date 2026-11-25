package db

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gitee.com/fisher_devops_cicd/rate-limiter-middleware/conf"
	"github.com/glebarez/sqlite"
	"github.com/toolkits/pkg/logger"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
	"gorm.io/gorm/schema"
)

const (
	// MigrationsDir holds the SQL files that create the tables of the
	// database. Every database type has its own sub directory, the files are
	// applied in alphabetical order on startup.
	MigrationsDir = "./migrations"

	// SlowThreshold is the duration after which a sql statement is logged
	// as a slow query.
	SlowThreshold = 200 * time.Millisecond
)

// New opens the database described by the config and creates the tables
// defined in the migration files of the database type. The dsn of a sqlite
// database is the path of the database file, the dsn of a mysql database is
// the connection string of the mysql driver, for example
// user:password@tcp(127.0.0.1:3306)/rate_limiter?charset=utf8mb4&parseTime=True&loc=Local
func New(cfg conf.DBConfig) (*gorm.DB, error) {
	var (
		dbType    string
		dialector gorm.Dialector
	)
	sqliteUsed := false

	switch normalizeDBType(cfg.DBType) {
	case "mysql":
		dbType = "mysql"
		dialector = mysql.Open(cfg.DSN)
	case "sqlite":
		dbType = "sqlite"
		sqliteUsed = true
		if err := createDBDir(cfg.DSN); err != nil {
			return nil, err
		}
		// The glebarez driver is a pure go implementation of sqlite, so the
		// binary stays static and does not need cgo to be enabled.
		dialector = sqlite.Open(cfg.DSN)
	default:
		return nil, fmt.Errorf("the database type %s is not supported, use mysql or sqlite", cfg.DBType)
	}

	logLevel := gormlogger.Warn
	if cfg.Debug {
		logLevel = gormlogger.Info
	}

	db, err := gorm.Open(dialector, &gorm.Config{
		NamingStrategy: schema.NamingStrategy{
			TablePrefix:   cfg.TablePrefix,
			SingularTable: true,
		},
		Logger: &tkitLogger{level: logLevel, slowThreshold: SlowThreshold},
	})
	if err != nil {
		// The dsn is not part of the message, the dsn of a mysql database
		// holds the credentials of the database.
		return nil, fmt.Errorf("failed to open the %s database: %v", dbType, err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("failed to get the database connection pool: %v", err)
	}

	if !sqliteUsed {
		sqlDB.SetMaxOpenConns(cfg.MaxOpenConns)
		sqlDB.SetMaxIdleConns(cfg.MaxIdleConns)
		sqlDB.SetConnMaxLifetime(time.Duration(cfg.MaxLifetime) * time.Second)
	} else {
		// For mysql database,create the rate limter config table manually.
		if err := migrate(db, filepath.Join(MigrationsDir, dbType)); err != nil {
			return nil, err
		}
	}

	return db, nil
}

// normalizeDBType maps the database type of the config to the name of the
// gorm dialector and of the migration directory. The config accepts sqlite3
// as well, that is the name of the sqlite3 driver.
func normalizeDBType(dbType string) string {
	switch strings.ToLower(strings.TrimSpace(dbType)) {
	case "mysql":
		return "mysql"
	case "sqlite", "sqlite3":
		return "sqlite"
	default:
		return ""
	}
}

// tkitLogger implements the logger interface of gorm and writes the sql
// traces of gorm into the log of the application.
type tkitLogger struct {
	level         gormlogger.LogLevel
	slowThreshold time.Duration
}

func (l *tkitLogger) LogMode(level gormlogger.LogLevel) gormlogger.Interface {
	newLogger := *l
	newLogger.level = level
	return &newLogger
}

// Info logs the message on the info level.
func (l *tkitLogger) Info(_ context.Context, msg string, args ...interface{}) {
	if l.level >= gormlogger.Info {
		logger.Info(formatMessage(msg, args))
	}
}

// Warn logs the message on the warning level.
func (l *tkitLogger) Warn(_ context.Context, msg string, args ...interface{}) {
	if l.level >= gormlogger.Warn {
		logger.Warning(formatMessage(msg, args))
	}
}

// Error logs the message on the error level.
func (l *tkitLogger) Error(_ context.Context, msg string, args ...interface{}) {
	if l.level >= gormlogger.Error {
		logger.Error(formatMessage(msg, args))
	}
}

// Trace logs the sql statement that was executed.
func (l *tkitLogger) Trace(_ context.Context, begin time.Time, fc func() (string, int64), err error) {
	if l.level <= gormlogger.Silent {
		return
	}

	elapsed := time.Since(begin)

	switch {
	case err != nil && l.level >= gormlogger.Error && !errors.Is(err, gormlogger.ErrRecordNotFound):
		sql, rows := fc()
		logger.Errorf("[gorm] sql: %s, rows affected: %d, elapsed: %v, error: %v", sql, rows, elapsed, err)
	case l.level >= gormlogger.Warn && l.slowThreshold > 0 && elapsed > l.slowThreshold:
		sql, rows := fc()
		logger.Warningf("[gorm] slow sql >= %v, sql: %s, rows affected: %d, elapsed: %v", l.slowThreshold, sql, rows, elapsed)
	case l.level >= gormlogger.Info:
		sql, rows := fc()
		logger.Infof("[gorm] sql: %s, rows affected: %d, elapsed: %v", sql, rows, elapsed)
	}
}

func formatMessage(msg string, args []interface{}) string {
	if len(args) == 0 {
		return msg
	}

	return fmt.Sprintf(msg, args...)
}

// migrate applies the SQL files of the given directory to the database.
func migrate(db *gorm.DB, dir string) error {
	files, err := filepath.Glob(filepath.Join(dir, "*.sql"))
	if err != nil {
		return fmt.Errorf("failed to list the migration files in %s: %v", dir, err)
	}

	if len(files) == 0 {
		logger.Warningf("no migration file found in %s, the tables of the database are not created", dir)
		return nil
	}

	for _, file := range files {
		script, err := os.ReadFile(file)
		if err != nil {
			return fmt.Errorf("failed to read the migration file %s: %v", file, err)
		}

		statements := splitStatements(string(script))
		if len(statements) == 0 {
			continue
		}

		if err := applyStatements(db, statements); err != nil {
			return fmt.Errorf("failed to apply the migration file %s: %v", file, err)
		}
	}

	return nil
}

func applyStatements(db *gorm.DB, statements []string) error {
	if db.Dialector.Name() == "sqlite" {
		return db.Transaction(func(tx *gorm.DB) error {
			for _, statement := range statements {
				if err := tx.Exec(statement).Error; err != nil {
					return fmt.Errorf("failed to run the statement %q: %v", statement, err)
				}
			}
			return nil
		})
	}

	for _, statement := range statements {
		if err := db.Exec(statement).Error; err != nil {
			return fmt.Errorf("failed to run the statement %q: %v", statement, err)
		}
	}

	return nil
}

// splitStatements splits a SQL script into the single statements it holds.
func splitStatements(script string) []string {
	var (
		statements  []string
		current     strings.Builder
		quote       byte
		lineComment bool
	)

	flush := func() {
		if statement := strings.TrimSpace(current.String()); statement != "" {
			statements = append(statements, statement)
		}
		current.Reset()
	}

	for i := 0; i < len(script); i++ {
		c := script[i]

		switch {
		case lineComment:
			if c == '\n' {
				lineComment = false
				current.WriteByte(c)
			}

		case quote != 0:
			current.WriteByte(c)
			if c == quote {
				quote = 0
			}

		case c == '-' && i+1 < len(script) && script[i+1] == '-':
			lineComment = true
			i++

		case c == '\'' || c == '"' || c == '`':
			quote = c
			current.WriteByte(c)

		case c == ';':
			flush()

		default:
			current.WriteByte(c)
		}
	}

	flush()

	return statements
}

// createDBDir creates the directory of the database file, sqlite does not
// create it on its own.
func createDBDir(dsn string) error {
	if strings.HasPrefix(dsn, "file:") || strings.Contains(dsn, "://") {
		return nil
	}

	dir := filepath.Dir(dsn)
	if dir == "" || dir == "." {
		return nil
	}

	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create the database directory %s: %v", dir, err)
	}

	return nil
}
