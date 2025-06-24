package config

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"strconv"
	"time"

	_ "github.com/joho/godotenv/autoload"
)

const ConfigFileName = "/data/options.json"

type Config struct {
	Server    ServerConfig    `json:"SERVER"`
	Database  DatabaseConfig  `json:"DATABASE"`
	Auth      AuthConfig      `json:"AUTH"`
	RateLimit RateLimitConfig `json:"RATE_LIMIT"`
	Cleanup   CleanupConfig   `json:"CLEANUP"`
	SSE       SSEConfig       `json:"SSE"`
}

type ServerConfig struct {
	Host string `json:"HOST"`
	Port string `json:"PORT"`
}

type DatabaseConfig struct {
	Path           string `json:"DB_PATH"`
	MigrationsPath string `json:"MIGRATIONS_PATH"`
}

type AuthConfig struct {
	JWTSecret      string `json:"JWT_SECRET"`
	InternalAPIKey string `json:"INTERNAL_API_KEY"`
}

type RateLimitConfig struct {
	WindowMs    int64 `json:"RATE_LIMIT_WINDOW"`
	MaxRequests int   `json:"RATE_LIMIT_MAX_REQUESTS"`
}

type CleanupConfig struct {
	Enabled        bool `json:"CLEANUP_ENABLED"`
	DaysToKeep     int  `json:"CLEANUP_DAYS"`
	TimeoutMinutes int  `json:"TASK_TIMEOUT_MINUTES"`
}

type SSEConfig struct {
	HeartbeatInterval time.Duration `json:"HEARTBEAT_INTERVAL"`
	ClientTimeout     time.Duration `json:"CLIENT_TIMEOUT"`
}

func Load(args []string) *Config {
	config := &Config{
		Server: ServerConfig{
			Host: getEnv("HOST", "0.0.0.0"),
			Port: getEnv("PORT", "8080"),
		},
		Database: DatabaseConfig{
			Path:           getEnv("DB_PATH", "./data/llm-proxy.db"),
			MigrationsPath: getEnv("DB_MIGRATIONS_PATH", "./migrations"),
		},
		Auth: AuthConfig{
			JWTSecret:      getEnv("JWT_SECRET", "dev-secret-key"),
			InternalAPIKey: getEnv("INTERNAL_API_KEY", "dev-internal-key"),
		},
		RateLimit: RateLimitConfig{
			WindowMs:    getEnvInt64("RATE_LIMIT_WINDOW", 86400000), // 24 hours
			MaxRequests: getEnvInt("RATE_LIMIT_MAX_REQUESTS", 100),
		},
		Cleanup: CleanupConfig{
			Enabled:        getEnvBool("CLEANUP_ENABLED", true),
			DaysToKeep:     getEnvInt("CLEANUP_DAYS", 7),
			TimeoutMinutes: getEnvInt("TASK_TIMEOUT_MINUTES", 30),
		},
		SSE: SSEConfig{
			HeartbeatInterval: getEnvDuration("SSE_HEARTBEAT_INTERVAL", 30*time.Second),
			ClientTimeout:     getEnvDuration("SSE_CLIENT_TIMEOUT", 5*time.Minute),
		},
	}

	var initFromFile = false

	if _, err := os.Stat(ConfigFileName); err == nil {
		jsonFile, err := os.Open(ConfigFileName)
		if err == nil {
			byteValue, _ := io.ReadAll(jsonFile)
			if err = json.Unmarshal(byteValue, &config); err == nil {
				initFromFile = true
			} else {
				fmt.Printf("error on unmarshal config from file %s\n", err.Error())
			}
		}
	}

	if !initFromFile {
		flags := flag.NewFlagSet(args[0], flag.ContinueOnError)

		flags.StringVar(&config.Server.Host, "host", lookupEnvOrString("HOST", config.Server.Host), "HOST")
		flags.StringVar(&config.Server.Port, "port", lookupEnvOrString("PORT", config.Server.Port), "PORT")
		flags.StringVar(&config.Database.Path, "dbPath", lookupEnvOrString("DB_PATH", config.Database.Path), "DB_PATH")
		flags.StringVar(&config.Database.MigrationsPath, "dbMigrationsPath", lookupEnvOrString("DB_MIGRATIONS_PATH", config.Database.MigrationsPath), "DB_MIGRATIONS_PATH")
		flags.StringVar(&config.Auth.JWTSecret, "jwtSecret", lookupEnvOrString("JWT_SECRET", config.Auth.JWTSecret), "JWT_SECRET")
		flags.StringVar(&config.Auth.InternalAPIKey, "internalAPIKey", lookupEnvOrString("INTERNAL_API_KEY", config.Auth.InternalAPIKey), "INTERNAL_API_KEY")
		flags.Int64Var(&config.RateLimit.WindowMs, "rateLimitWindow", lookupEnvOrInt64("RATE_LIMIT_WINDOW", config.RateLimit.WindowMs), "RATE_LIMIT_WINDOW")
		flags.IntVar(&config.RateLimit.MaxRequests, "rateLimitMaxRequests", lookupEnvOrInt("RATE_LIMIT_MAX_REQUESTS", config.RateLimit.MaxRequests), "RATE_LIMIT_MAX_REQUESTS")
		flags.BoolVar(&config.Cleanup.Enabled, "cleanupEnabled", lookupEnvOrBool("CLEANUP_ENABLED", config.Cleanup.Enabled), "CLEANUP_ENABLED")
		flags.IntVar(&config.Cleanup.DaysToKeep, "cleanupDays", lookupEnvOrInt("CLEANUP_DAYS", config.Cleanup.DaysToKeep), "CLEANUP_DAYS")
		flags.IntVar(&config.Cleanup.TimeoutMinutes, "taskTimeoutMinutes", lookupEnvOrInt("TASK_TIMEOUT_MINUTES", config.Cleanup.TimeoutMinutes), "TASK_TIMEOUT_MINUTES")
		flags.DurationVar(&config.SSE.HeartbeatInterval, "sseHeartbeatInterval", lookupEnvOrDuration("SSE_HEARTBEAT_INTERVAL", config.SSE.HeartbeatInterval), "SSE_HEARTBEAT_INTERVAL")
		flags.DurationVar(&config.SSE.ClientTimeout, "sseClientTimeout", lookupEnvOrDuration("SSE_CLIENT_TIMEOUT", config.SSE.ClientTimeout), "SSE_CLIENT_TIMEOUT")

		// flags.BoolVar(&config.Debug, "debug", lookupEnvOrBool("DEBUG", config.Debug), "Debug")

		if err := flags.Parse(args[1:]); err != nil {
			return config
		}
	}

	log.Printf("Loaded configuration: %+v", config)

	return config
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func getEnvInt(key string, defaultValue int) int {
	if value := os.Getenv(key); value != "" {
		if i, err := strconv.Atoi(value); err == nil {
			return i
		}
	}
	return defaultValue
}

func getEnvInt64(key string, defaultValue int64) int64 {
	if value := os.Getenv(key); value != "" {
		if i, err := strconv.ParseInt(value, 10, 64); err == nil {
			return i
		}
	}
	return defaultValue
}

func getEnvBool(key string, defaultValue bool) bool {
	if value := os.Getenv(key); value != "" {
		if b, err := strconv.ParseBool(value); err == nil {
			return b
		}
	}
	return defaultValue
}

func getEnvDuration(key string, defaultValue time.Duration) time.Duration {
	if value := os.Getenv(key); value != "" {
		if d, err := time.ParseDuration(value); err == nil {
			return d
		}
	}
	return defaultValue
}
