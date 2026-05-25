package config

import (
	"os"

	"github.com/joho/godotenv"
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
	_ = godotenv.Load(".env")

	cfg := Config{
		DatabaseURL: os.Getenv("DATABASE_URL"),
		Hikvision: HikvisionConfig{
			Host:     os.Getenv("HIKVISION_HOST"),
			Username: os.Getenv("HIKVISION_USERNAME"),
			Password: os.Getenv("HIKVISION_PASSWORD"),
		},
		Inspireface: InspirefaceConfig{
			Host: os.Getenv("INSPIREFACE_HOST"),
		},
	}

	return cfg, nil
}
