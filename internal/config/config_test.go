package config

import (
	"testing"
	"time"
)

func TestLoad(t *testing.T) {
	tests := []struct {
		name      string
		setup     func(*testing.T)
		wantError bool
		validate  func(*testing.T, *Config)
	}{
		{
			name: "valid config - all fields filled",
			setup: func(t *testing.T) {
				t.Setenv("ENVIRONMENT", "development")
				t.Setenv("DOMAIN", "example.com")
				t.Setenv("NIMBUS_STORAGE_CLASS", "nfs")
				t.Setenv("DB_HOST", "localhost")
				t.Setenv("DB_PORT", "5432")
				t.Setenv("DB_NAME", "nimbus")
				t.Setenv("DB_USER", "nimbus")
				t.Setenv("DB_PASSWORD", "password")
			},
			wantError: false,
			validate: func(t *testing.T, cfg *Config) {
				if cfg.Environment != "development" {
					t.Errorf("expected Environment %s, got %s", "development", cfg.Environment)
				}
				if cfg.Domain != "example.com" {
					t.Errorf("expected Domain %s, got %s", "example.com", cfg.Domain)
				}
				if cfg.NimbusStorageClass != "nfs" {
					t.Errorf("expected NimbusStorageClass %s, got %s", "nfs", cfg.NimbusStorageClass)
				}
				if cfg.Database.Host != "localhost" {
					t.Errorf("expected DB_HOST %s, got %s", "localhost", cfg.Database.Host)
				}
				if cfg.Database.Port != "5432" {
					t.Errorf("expected DB_PORT %s, got %s", "5432", cfg.Database.Port)
				}
				if cfg.Database.Name != "nimbus" {
					t.Errorf("expected DB_NAME %s, got %s", "nimbus", cfg.Database.Name)
				}
				if cfg.Database.User != "nimbus" {
					t.Errorf("expected DB_USER %s, got %s", "nimbus", cfg.Database.User)
				}
				if cfg.Database.Password != "password" {
					t.Errorf("expected DB_PASSWORD %s, got %s", "password", cfg.Database.Password)
				}
			},
		},
		{
			name: "valid config - production environment",
			setup: func(t *testing.T) {
				t.Setenv("ENVIRONMENT", "production")
				t.Setenv("DOMAIN", "example.com")
				t.Setenv("DB_HOST", "db.example.com")
				t.Setenv("DB_PORT", "5432")
				t.Setenv("DB_NAME", "nimbus")
				t.Setenv("DB_USER", "nimbus")
				t.Setenv("DB_PASSWORD", "password")
			},
			wantError: false,
			validate: func(t *testing.T, cfg *Config) {
				if cfg.Environment != "production" {
					t.Errorf("expected Environment %s, got %s", "production", cfg.Environment)
				}
			},
		},
		{
			name: "valid config - defaults to development",
			setup: func(t *testing.T) {
				t.Setenv("DOMAIN", "example.com")
				t.Setenv("DB_HOST", "localhost")
				t.Setenv("DB_NAME", "nimbus")
				t.Setenv("DB_USER", "nimbus")
				t.Setenv("DB_PASSWORD", "password")
			},
			wantError: false,
			validate: func(t *testing.T, cfg *Config) {
				if cfg.Environment != "development" {
					t.Errorf("expected default Environment %s, got %s", "development", cfg.Environment)
				}
				if cfg.Database.Port != "5432" {
					t.Errorf("expected default DB_PORT %s, got %s", "5432", cfg.Database.Port)
				}
			},
		},
		{
			name: "missing required field - DB_HOST",
			setup: func(t *testing.T) {
				t.Setenv("DOMAIN", "example.com")
				t.Setenv("DB_PORT", "5432")
				t.Setenv("DB_NAME", "nimbus")
				t.Setenv("DB_USER", "nimbus")
				t.Setenv("DB_PASSWORD", "password")
			},
			wantError: true,
		},
		{
			name: "missing required field - DOMAIN",
			setup: func(t *testing.T) {
				t.Setenv("DB_HOST", "localhost")
				t.Setenv("DB_PORT", "5432")
				t.Setenv("DB_NAME", "nimbus")
				t.Setenv("DB_USER", "nimbus")
				t.Setenv("DB_PASSWORD", "password")
			},
			wantError: true,
		},
		{
			name: "missing required field - DB_NAME",
			setup: func(t *testing.T) {
				t.Setenv("DOMAIN", "example.com")
				t.Setenv("DB_HOST", "localhost")
				t.Setenv("DB_PORT", "5432")
				t.Setenv("DB_USER", "nimbus")
				t.Setenv("DB_PASSWORD", "password")
			},
			wantError: true,
		},
		{
			name: "missing required field - DB_USER",
			setup: func(t *testing.T) {
				t.Setenv("DOMAIN", "example.com")
				t.Setenv("DB_HOST", "localhost")
				t.Setenv("DB_PORT", "5432")
				t.Setenv("DB_NAME", "nimbus")
				t.Setenv("DB_PASSWORD", "password")
			},
			wantError: true,
		},
		{
			name: "missing required field - DB_PASSWORD",
			setup: func(t *testing.T) {
				t.Setenv("DOMAIN", "example.com")
				t.Setenv("DB_HOST", "localhost")
				t.Setenv("DB_PORT", "5432")
				t.Setenv("DB_NAME", "nimbus")
				t.Setenv("DB_USER", "nimbus")
			},
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.setup(t)
			cfg, err := Load()

			if tt.wantError {
				if err == nil {
					t.Error("expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if tt.validate != nil {
				tt.validate(t, cfg)
			}
		})
	}
}

func TestImageRefreshInterval(t *testing.T) {
	for _, tc := range []struct {
		value     string
		want      time.Duration
		wantError bool
	}{
		{"", 5 * time.Second, false},
		{"20s", 20 * time.Second, false},
		{"1m", time.Minute, false},
		{"0s", 0, true},
		{"-1s", 0, true},
		{"invalid", 0, true},
	} {
		t.Run(tc.value, func(t *testing.T) {
			t.Setenv("DOMAIN", "example.com")
			t.Setenv("DB_HOST", "localhost")
			t.Setenv("DB_NAME", "nimbus")
			t.Setenv("DB_USER", "nimbus")
			t.Setenv("DB_PASSWORD", "password")
			t.Setenv("NIMBUS_IMAGE_REFRESH_INTERVAL", tc.value)
			cfg, err := Load()
			if (err != nil) != tc.wantError {
				t.Fatalf("error = %v", err)
			}
			if !tc.wantError && cfg.ImageRefreshInterval != tc.want {
				t.Fatalf("interval = %s, want %s", cfg.ImageRefreshInterval, tc.want)
			}
		})
	}
}
