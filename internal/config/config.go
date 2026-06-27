package config

import (
	"fmt"
	"os"
	"strings"
)

type Config struct {
	HTTPAddr    string
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
	PackPath string
}

func Load() (Config, error) {
	cfg := Config{
		HTTPAddr:    getEnv("HTTP_ADDR", ":5090"),
		DatabaseURL: getEnv("DATABASE_URL", ""),

		Hikvision: HikvisionConfig{
			Host:     getEnv("HIKVISION_HOST", ""),
			Username: getEnv("HIKVISION_USERNAME", ""),
			Password: getEnv("HIKVISION_PASSWORD", ""),
		},

		Inspireface: InspirefaceConfig{
			PackPath: getEnv("INSPIREFACE_PACK_PATH", ".sdk/models/Megatron"),
		},
	}

	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}

	return cfg, nil
}

func (c Config) Validate() error {
	if strings.TrimSpace(c.HTTPAddr) == "" {
		return fmt.Errorf("HTTP_ADDR cannot be empty")
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
