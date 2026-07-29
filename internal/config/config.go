package config

import (
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	defaultHTTPAddress     = ":5090"
	defaultRequestTimeout  = 30 * time.Second
	defaultShutdownTimeout = 10 * time.Second
)

// Config 是应用唯一的配置入口。摄像头和处理器使用 map，以便 main 根据
// type 将 Options 原样交给各自的构造函数。
type Config struct {
	DB         DBConfig                 `yaml:"db"`
	Server     ServerConfig             `yaml:"server"`
	App        AppConfig                `yaml:"app"`
	Cameras    map[string]ComponentSpec `yaml:"cameras"`
	Processors map[string]ProcessorSpec `yaml:"processors"`
}

type DBConfig struct {
	DSN string `yaml:"dsn"`
}

type ServerConfig struct {
	Address         string        `yaml:"address"`
	Port            int           `yaml:"port"`
	RequestTimeout  time.Duration `yaml:"-"`
	ShutdownTimeout time.Duration `yaml:"-"`

	RequestTimeoutText  string `yaml:"request_timeout"`
	ShutdownTimeoutText string `yaml:"shutdown_timeout"`
}

type AppConfig struct {
	DefaultCamera        string        `yaml:"default_camera"`
	SimilarityThreshold  float64       `yaml:"similarity_threshold"`
	SignInInterval       time.Duration `yaml:"-"`
	SignInCooldown       time.Duration `yaml:"-"`
	EnrollmentTimeout    time.Duration `yaml:"-"`
	EnrollmentSamples    int           `yaml:"enrollment_samples"`
	EnrollmentMinQuality float64       `yaml:"enrollment_min_quality"`

	SignInIntervalText    string `yaml:"sign_in_interval"`
	SignInCooldownText    string `yaml:"sign_in_cooldown"`
	EnrollmentTimeoutText string `yaml:"enrollment_timeout"`
}

type ComponentSpec struct {
	Type    string         `yaml:"type"`
	Enabled bool           `yaml:"enabled"`
	Options map[string]any `yaml:"options"`
}

type ProcessorSpec struct {
	Type    string         `yaml:"type"`
	Enabled bool           `yaml:"enabled"`
	Pools   PoolSpec       `yaml:"pools"`
	Options map[string]any `yaml:"options"`
}

type PoolSpec struct {
	Workers   int `yaml:"workers"`
	QueueSize int `yaml:"queue_size"`
}

// Load 从指定路径读取 YAML，并保留 DATABASE_URL、HTTP_ADDR 和
// INSPIREFACE_PACK_PATH 三个部署环境覆盖项。
func Load(path string) (*Config, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, errors.New("config path cannot be empty")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config file %q: %w", path, err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config file %q: %w", path, err)
	}
	cfg.applyDefaults()
	cfg.applyEnvironment()
	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("validate config file %q: %w", path, err)
	}
	return &cfg, nil
}

func (c *Config) applyDefaults() {
	if strings.TrimSpace(c.Server.Address) == "" {
		if c.Server.Port > 0 {
			c.Server.Address = ":" + strconv.Itoa(c.Server.Port)
		} else {
			c.Server.Address = defaultHTTPAddress
		}
	}
	c.Server.RequestTimeout = parseDuration(c.Server.RequestTimeoutText, defaultRequestTimeout)
	c.Server.ShutdownTimeout = parseDuration(c.Server.ShutdownTimeoutText, defaultShutdownTimeout)

	if c.App.SimilarityThreshold == 0 {
		c.App.SimilarityThreshold = 0.45
	}
	if c.App.EnrollmentSamples == 0 {
		c.App.EnrollmentSamples = 5
	}
	if c.App.EnrollmentMinQuality == 0 {
		c.App.EnrollmentMinQuality = 0.45
	}
	c.App.SignInInterval = parseDuration(c.App.SignInIntervalText, 500*time.Millisecond)
	c.App.SignInCooldown = parseDuration(c.App.SignInCooldownText, 30*time.Second)
	c.App.EnrollmentTimeout = parseDuration(c.App.EnrollmentTimeoutText, 15*time.Second)

	if c.Cameras == nil {
		c.Cameras = make(map[string]ComponentSpec)
	}
	if c.Processors == nil {
		c.Processors = make(map[string]ProcessorSpec)
	}
}

func (c *Config) applyEnvironment() {
	if value := strings.TrimSpace(os.Getenv("DATABASE_URL")); value != "" {
		c.DB.DSN = value
	}
	if value := strings.TrimSpace(os.Getenv("HTTP_ADDR")); value != "" {
		c.Server.Address = value
	}
	if value := strings.TrimSpace(os.Getenv("INSPIREFACE_PACK_PATH")); value != "" {
		for id, spec := range c.Processors {
			if strings.EqualFold(spec.Type, "inspireface") {
				if spec.Options == nil {
					spec.Options = make(map[string]any)
				}
				spec.Options["pack_path"] = value
				c.Processors[id] = spec
			}
		}
	}
}

func (c *Config) validate() error {
	if _, _, err := net.SplitHostPort(c.Server.Address); err != nil {
		return fmt.Errorf("server.address %q is invalid: %w", c.Server.Address, err)
	}
	if strings.TrimSpace(c.DB.DSN) == "" {
		return errors.New("db.dsn cannot be empty")
	}
	if c.Server.RequestTimeout <= 0 || c.Server.ShutdownTimeout <= 0 {
		return errors.New("server timeouts must be positive")
	}
	if c.App.SimilarityThreshold <= 0 || c.App.SimilarityThreshold > 1 {
		return errors.New("app.similarity_threshold must be in (0, 1]")
	}
	if c.App.SignInInterval <= 0 || c.App.SignInCooldown <= 0 || c.App.EnrollmentTimeout <= 0 {
		return errors.New("app intervals must be positive")
	}
	if c.App.EnrollmentSamples <= 0 {
		return errors.New("app.enrollment_samples must be positive")
	}
	if c.App.EnrollmentMinQuality < 0 || c.App.EnrollmentMinQuality > 1 {
		return errors.New("app.enrollment_min_quality must be in [0, 1]")
	}

	enabledCameras := 0
	for id, spec := range c.Cameras {
		if strings.TrimSpace(id) == "" {
			return errors.New("camera id cannot be empty")
		}
		if !spec.Enabled {
			continue
		}
		enabledCameras++
		if strings.TrimSpace(spec.Type) == "" {
			return fmt.Errorf("camera %q type cannot be empty", id)
		}
	}
	if enabledCameras > 0 {
		if strings.TrimSpace(c.App.DefaultCamera) == "" {
			return errors.New("app.default_camera is required when a camera is enabled")
		}
		spec, ok := c.Cameras[c.App.DefaultCamera]
		if !ok || !spec.Enabled {
			return fmt.Errorf("app.default_camera %q is not enabled", c.App.DefaultCamera)
		}
	}

	enabledRecognition := false
	for id, spec := range c.Processors {
		if strings.TrimSpace(id) == "" {
			return errors.New("processor id cannot be empty")
		}
		if spec.Enabled && strings.EqualFold(spec.Type, "inspireface") {
			enabledRecognition = true
		}
		if spec.Pools.Workers < 0 || spec.Pools.QueueSize < 0 {
			return fmt.Errorf("processor %q pool values cannot be negative", id)
		}
	}
	if !enabledRecognition {
		return errors.New("an enabled inspireface processor is required")
	}
	return nil
}

func parseDuration(value string, fallback time.Duration) time.Duration {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	duration, err := time.ParseDuration(value)
	if err != nil {
		return 0
	}
	return duration
}
