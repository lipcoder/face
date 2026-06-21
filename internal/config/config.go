package config

import (
	"fmt"
	"os"
	"strings"
)

type Config struct {
	DatabaseURL string
	Hikvision   HikvisionConfig
	Inspireface InspirefaceConfig
}

type HikvisionConfig struct {
	Host     string
	Username string
	Password string
}

type InspirefaceConfig struct {
	Host string
}

func Load() (Config, error) {
	cfg := Config{
		DatabaseURL: getEnv("DATABASE_URL", ""),

		Hikvision: HikvisionConfig{
			Host:     getEnv("HIKVISION_HOST", ""),
			Username: getEnv("HIKVISION_USERNAME", ""),
			Password: getEnv("HIKVISION_PASSWORD", ""),
		},

		Inspireface: InspirefaceConfig{
			Host: getEnv("INSPIREFACE_HOST", ""),
		},
	}

	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}

	return cfg, nil
}

func (c Config) Validate() error {
	if strings.TrimSpace(c.DatabaseURL) == "" {
		return fmt.Errorf("DATABASE_URL cannot be empty")
	}

	return nil
}

func getEnv(key string, fallback string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	return value
}
