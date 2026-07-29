package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadMapsAndDefaults(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	t.Setenv("HTTP_ADDR", "")
	t.Setenv("INSPIREFACE_PACK_PATH", "")
	path := filepath.Join(t.TempDir(), "config.yaml")
	data := []byte(`
db:
  dsn: postgres://test
server:
  port: 5090
app:
  default_camera: local
cameras:
  local:
    type: local
    enabled: true
    options:
      device_id: 0
processors:
  face:
    type: inspireface
    enabled: true
    pools:
      workers: 2
      queue_size: 8
    options:
      pack_path: model
`)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Server.Address != ":5090" {
		t.Fatalf("address = %q", cfg.Server.Address)
	}
	if _, ok := cfg.Cameras["local"]; !ok {
		t.Fatal("local camera was not loaded into map")
	}
	if cfg.App.EnrollmentSamples != 5 {
		t.Fatalf("enrollment samples = %d", cfg.App.EnrollmentSamples)
	}
}

func TestLoadRejectsDisabledDefaultCamera(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	path := filepath.Join(t.TempDir(), "config.yaml")
	data := []byte(`
db:
  dsn: postgres://test
app:
  default_camera: local
cameras:
  local:
    type: local
    enabled: false
  other:
    type: rtsp
    enabled: true
processors:
  face:
    type: inspireface
    enabled: true
`)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("expected disabled default camera error")
	}
}
