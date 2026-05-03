package config

import (
	"strings"
	"testing"
)

func TestLoadConfig(t *testing.T) {
	tests := []struct {
		name      string
		setup     func(*testing.T)
		wantError bool
		validate  func(*testing.T, *EnvConfig)
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
			validate: func(t *testing.T, config *EnvConfig) {
				if config.Environment != "development" {
					t.Errorf("expected Environment %s, got %s", "development", config.Environment)
				}
				if config.Domain != "example.com" {
					t.Errorf("expected Domain %s, got %s", "example.com", config.Domain)
				}
				if config.NimbusStorageClass != "nfs" {
					t.Errorf("expected NimbusStorageClass %s, got %s", "nfs", config.NimbusStorageClass)
				}
				if config.Database.Host != "localhost" {
					t.Errorf("expected DB_HOST %s, got %s", "localhost", config.Database.Host)
				}
				if config.Database.Port != "5432" {
					t.Errorf("expected DB_PORT %s, got %s", "5432", config.Database.Port)
				}
				if config.Database.Name != "nimbus" {
					t.Errorf("expected DB_NAME %s, got %s", "nimbus", config.Database.Name)
				}
				if config.Database.User != "nimbus" {
					t.Errorf("expected DB_USER %s, got %s", "nimbus", config.Database.User)
				}
				if config.Database.Password != "password" {
					t.Errorf("expected DB_PASSWORD %s, got %s", "password", config.Database.Password)
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
			validate: func(t *testing.T, config *EnvConfig) {
				if config.Environment != "production" {
					t.Errorf("expected Environment %s, got %s", "production", config.Environment)
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
			validate: func(t *testing.T, config *EnvConfig) {
				if config.Environment != "development" {
					t.Errorf("expected default Environment %s, got %s", "development", config.Environment)
				}
				if config.Database.Port != "5432" {
					t.Errorf("expected default DB_PORT %s, got %s", "5432", config.Database.Port)
				}
			},
		},
		{
			name: "missing required field - DB_HOST",
			setup: func(t *testing.T) {
				t.Setenv("ENVIRONMENT", "development")
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
				t.Setenv("ENVIRONMENT", "development")
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
				t.Setenv("ENVIRONMENT", "development")
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
				t.Setenv("ENVIRONMENT", "development")
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
				t.Setenv("ENVIRONMENT", "development")
				t.Setenv("DOMAIN", "example.com")
				t.Setenv("DB_HOST", "localhost")
				t.Setenv("DB_PORT", "5432")
				t.Setenv("DB_NAME", "nimbus")
				t.Setenv("DB_USER", "nimbus")
			},
			wantError: true,
		},
		{
			name: "invalid environment value",
			setup: func(t *testing.T) {
				t.Setenv("ENVIRONMENT", "staging")
				t.Setenv("DOMAIN", "example.com")
				t.Setenv("DB_HOST", "localhost")
				t.Setenv("DB_PORT", "5432")
				t.Setenv("DB_NAME", "nimbus")
				t.Setenv("DB_USER", "nimbus")
				t.Setenv("DB_PASSWORD", "password")
			},
			wantError: true,
		},
		{
			name: "invalid port - not a number",
			setup: func(t *testing.T) {
				t.Setenv("ENVIRONMENT", "development")
				t.Setenv("DOMAIN", "example.com")
				t.Setenv("DB_HOST", "localhost")
				t.Setenv("DB_PORT", "not-a-port")
				t.Setenv("DB_NAME", "nimbus")
				t.Setenv("DB_USER", "nimbus")
				t.Setenv("DB_PASSWORD", "password")
			},
			wantError: true,
		},
		{
			name: "invalid port - zero",
			setup: func(t *testing.T) {
				t.Setenv("ENVIRONMENT", "development")
				t.Setenv("DOMAIN", "example.com")
				t.Setenv("DB_HOST", "localhost")
				t.Setenv("DB_PORT", "0")
				t.Setenv("DB_NAME", "nimbus")
				t.Setenv("DB_USER", "nimbus")
				t.Setenv("DB_PASSWORD", "password")
			},
			wantError: true,
		},
		{
			name: "invalid port - too large",
			setup: func(t *testing.T) {
				t.Setenv("ENVIRONMENT", "development")
				t.Setenv("DOMAIN", "example.com")
				t.Setenv("DB_HOST", "localhost")
				t.Setenv("DB_PORT", "99999")
				t.Setenv("DB_NAME", "nimbus")
				t.Setenv("DB_USER", "nimbus")
				t.Setenv("DB_PASSWORD", "password")
			},
			wantError: true,
		},
		{
			name: "invalid domain - special hostname",
			setup: func(t *testing.T) {
				t.Setenv("ENVIRONMENT", "development")
				t.Setenv("DOMAIN", "localhost!")
				t.Setenv("DB_HOST", "localhost")
				t.Setenv("DB_PORT", "5432")
				t.Setenv("DB_NAME", "nimbus")
				t.Setenv("DB_USER", "nimbus")
				t.Setenv("DB_PASSWORD", "password")
			},
			wantError: true,
		},
		{
			name: "invalid hostname - special characters",
			setup: func(t *testing.T) {
				t.Setenv("ENVIRONMENT", "development")
				t.Setenv("DOMAIN", "example.com")
				t.Setenv("DB_HOST", "host@name!")
				t.Setenv("DB_PORT", "5432")
				t.Setenv("DB_NAME", "nimbus")
				t.Setenv("DB_USER", "nimbus")
				t.Setenv("DB_PASSWORD", "password")
			},
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.setup(t)
			config, err := LoadEnvConfig()

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
				tt.validate(t, &config)
			}
		})
	}
}

func TestToEnvName(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "simple field",
			input: "Config.Environment",
			want:  "ENVIRONMENT",
		},
		{
			name:  "camelCase field",
			input: "Config.NimbusStorageClass",
			want:  "NIMBUS_STORAGE_CLASS",
		},
		{
			name:  "database field - Host",
			input: "Config.Database.Host",
			want:  "DB_HOST",
		},
		{
			name:  "database field - Port",
			input: "Config.Database.Port",
			want:  "DB_PORT",
		},
		{
			name:  "database field - Name",
			input: "Config.Database.Name",
			want:  "DB_NAME",
		},
		{
			name:  "database field - User",
			input: "Config.Database.User",
			want:  "DB_USER",
		},
		{
			name:  "database field - Password",
			input: "Config.Database.Password",
			want:  "DB_PASSWORD",
		},
		{
			name:  "single letter",
			input: "Config.A",
			want:  "A",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := toEnvName(tt.input)
			if got != tt.want {
				t.Errorf("toEnvName(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestErrorMessages(t *testing.T) {
	tests := []struct {
		name          string
		setup         func(*testing.T)
		expectedError string
	}{
		{
			name: "friendly error for invalid environment",
			setup: func(t *testing.T) {
				t.Setenv("ENVIRONMENT", "staging")
				t.Setenv("DOMAIN", "example.com")
				t.Setenv("DB_HOST", "localhost")
				t.Setenv("DB_PORT", "5432")
				t.Setenv("DB_NAME", "nimbus")
				t.Setenv("DB_USER", "nimbus")
				t.Setenv("DB_PASSWORD", "password")
			},
			expectedError: "invalid ENVIRONMENT (staging) - expected one of: development production",
		},
		{
			name: "friendly error for invalid domain",
			setup: func(t *testing.T) {
				t.Setenv("ENVIRONMENT", "development")
				t.Setenv("DOMAIN", "localhost!")
				t.Setenv("DB_HOST", "localhost")
				t.Setenv("DB_PORT", "5432")
				t.Setenv("DB_NAME", "nimbus")
				t.Setenv("DB_USER", "nimbus")
				t.Setenv("DB_PASSWORD", "password")
			},
			expectedError: "invalid DOMAIN (localhost!) - expected a valid hostname",
		},
		{
			name: "friendly error for invalid hostname",
			setup: func(t *testing.T) {
				t.Setenv("ENVIRONMENT", "development")
				t.Setenv("DOMAIN", "example.com")
				t.Setenv("DB_HOST", "host@name!")
				t.Setenv("DB_PORT", "5432")
				t.Setenv("DB_NAME", "nimbus")
				t.Setenv("DB_USER", "nimbus")
				t.Setenv("DB_PASSWORD", "password")
			},
			expectedError: "invalid DB_HOST (host@name!) - expected a valid hostname",
		},
		{
			name: "friendly error for invalid port",
			setup: func(t *testing.T) {
				t.Setenv("ENVIRONMENT", "development")
				t.Setenv("DOMAIN", "example.com")
				t.Setenv("DB_HOST", "localhost")
				t.Setenv("DB_PORT", "99999")
				t.Setenv("DB_NAME", "nimbus")
				t.Setenv("DB_USER", "nimbus")
				t.Setenv("DB_PASSWORD", "password")
			},
			expectedError: "invalid DB_PORT (99999) - expected an int between 1 and 65535",
		},
		{
			name: "friendly error for missing required field",
			setup: func(t *testing.T) {
				t.Setenv("ENVIRONMENT", "development")
				t.Setenv("DOMAIN", "example.com")
				t.Setenv("DB_HOST", "localhost")
				t.Setenv("DB_PORT", "5432")
				t.Setenv("DB_NAME", "nimbus")
				t.Setenv("DB_USER", "nimbus")
			},
			expectedError: "environment variable DB_PASSWORD is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.setup(t)
			_, err := LoadEnvConfig()

			if err == nil {
				t.Fatal("expected error, got nil")
			}

			if !strings.Contains(err.Error(), tt.expectedError) {
				t.Errorf("expected error to contain %q, got %q", tt.expectedError, err.Error())
			}
		})
	}
}

func TestValidatePort(t *testing.T) {
	tests := []struct {
		name      string
		setup     func(*testing.T)
		wantError bool
	}{
		{
			name: "valid port - 5432",
			setup: func(t *testing.T) {
				t.Setenv("ENVIRONMENT", "development")
				t.Setenv("DOMAIN", "example.com")
				t.Setenv("DB_HOST", "localhost")
				t.Setenv("DB_PORT", "5432")
				t.Setenv("DB_NAME", "nimbus")
				t.Setenv("DB_USER", "nimbus")
				t.Setenv("DB_PASSWORD", "password")
			},
			wantError: false,
		},
		{
			name: "valid port - 1",
			setup: func(t *testing.T) {
				t.Setenv("ENVIRONMENT", "development")
				t.Setenv("DOMAIN", "example.com")
				t.Setenv("DB_HOST", "localhost")
				t.Setenv("DB_PORT", "1")
				t.Setenv("DB_NAME", "nimbus")
				t.Setenv("DB_USER", "nimbus")
				t.Setenv("DB_PASSWORD", "password")
			},
			wantError: false,
		},
		{
			name: "valid port - 65535",
			setup: func(t *testing.T) {
				t.Setenv("ENVIRONMENT", "development")
				t.Setenv("DOMAIN", "example.com")
				t.Setenv("DB_HOST", "localhost")
				t.Setenv("DB_PORT", "65535")
				t.Setenv("DB_NAME", "nimbus")
				t.Setenv("DB_USER", "nimbus")
				t.Setenv("DB_PASSWORD", "password")
			},
			wantError: false,
		},
		{
			name: "invalid port - zero",
			setup: func(t *testing.T) {
				t.Setenv("ENVIRONMENT", "development")
				t.Setenv("DOMAIN", "example.com")
				t.Setenv("DB_HOST", "localhost")
				t.Setenv("DB_PORT", "0")
				t.Setenv("DB_NAME", "nimbus")
				t.Setenv("DB_USER", "nimbus")
				t.Setenv("DB_PASSWORD", "password")
			},
			wantError: true,
		},
		{
			name: "invalid port - negative",
			setup: func(t *testing.T) {
				t.Setenv("ENVIRONMENT", "development")
				t.Setenv("DOMAIN", "example.com")
				t.Setenv("DB_HOST", "localhost")
				t.Setenv("DB_PORT", "-1")
				t.Setenv("DB_NAME", "nimbus")
				t.Setenv("DB_USER", "nimbus")
				t.Setenv("DB_PASSWORD", "password")
			},
			wantError: true,
		},
		{
			name: "invalid port - greater than 65535",
			setup: func(t *testing.T) {
				t.Setenv("ENVIRONMENT", "development")
				t.Setenv("DOMAIN", "example.com")
				t.Setenv("DB_HOST", "localhost")
				t.Setenv("DB_PORT", "65536")
				t.Setenv("DB_NAME", "nimbus")
				t.Setenv("DB_USER", "nimbus")
				t.Setenv("DB_PASSWORD", "password")
			},
			wantError: true,
		},
		{
			name: "invalid port - not a number",
			setup: func(t *testing.T) {
				t.Setenv("ENVIRONMENT", "development")
				t.Setenv("DOMAIN", "example.com")
				t.Setenv("DB_HOST", "localhost")
				t.Setenv("DB_PORT", "abc")
				t.Setenv("DB_NAME", "nimbus")
				t.Setenv("DB_USER", "nimbus")
				t.Setenv("DB_PASSWORD", "password")
			},
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.setup(t)
			_, err := LoadEnvConfig()

			if tt.wantError {
				if err == nil {
					t.Error("expected error for invalid port, got nil")
				} else if !strings.Contains(err.Error(), "DB_PORT") {
					t.Errorf("expected error message to mention DB_PORT, got: %v", err)
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}
