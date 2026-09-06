// Package config contains function for loading and managing the nimbus config.
package config

import (
	"fmt"

	"github.com/caarlos0/env/v11"
)

type Database struct {
	Host     string `env:"DB_HOST,required"`
	Port     string `env:"DB_PORT" envDefault:"5432"`
	Name     string `env:"DB_NAME,required"`
	User     string `env:"DB_USER,required"`
	Password string `env:"DB_PASSWORD,required"`
}

type Config struct {
	GatewayName         string   `env:"ENVOY_GATEWAY_NAME" envDefault:"edge"`
	GatewayNamespace    string   `env:"ENVOY_GATEWAY_NAMESPACE" envDefault:"envoy-gateway-system"`
	GatewayHTTPListener string   `env:"ENVOY_HTTP_LISTENER" envDefault:"http"`
	RouteHelperImage    string   `env:"NIMBUS_ROUTE_HELPER_IMAGE"`
	RouteReadyTimeout   int      `env:"ENVOY_READY_TIMEOUT_SECONDS" envDefault:"180"`
	Environment         string   `env:"ENVIRONMENT" envDefault:"development"`
	Domain              string   `env:"DOMAIN,required"`
	NimbusStorageClass  string   `env:"NIMBUS_STORAGE_CLASS"`
	LogLevel            string   `env:"LOG_LEVEL" envDefault:"debug"`
	Database            Database `envPrefix:""`
}

func Load() (*Config, error) {
	var cfg Config
	if err := env.Parse(&cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}
	return &cfg, nil
}
