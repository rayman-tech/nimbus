// Package config contains function for loading and managing the nimbus config.
package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/go-playground/locales/en"
	ut "github.com/go-playground/universal-translator"
	"github.com/go-playground/validator/v10"
	en_translations "github.com/go-playground/validator/v10/translations/en"
)

// use a single instance, it caches struct info
var (
	uni      *ut.UniversalTranslator
	validate *validator.Validate
)

func init() {
	en := en.New()
	uni = ut.New(en, en)
	validate = validator.New(validator.WithRequiredStructEnabled())
	validate.RegisterValidation("port", validatePort)
}

type Database struct {
	Host     string `validate:"required,hostname_rfc1123"`
	Port     string `validate:"required,port"`
	Name     string `validate:"required"`
	User     string `validate:"required"`
	Password string `validate:"required"`
}

type Config struct {
	Environment        string `validate:"omitempty,oneof=development production"`
	Domain             string `validate:"required,hostname_rfc1123"`
	NimbusStorageClass string
	Database           Database `validate:"required"`
}

func loadWithDefault(key, def string) string {
	val := os.Getenv(key)
	if val == "" {
		val = def
	}
	return val
}

func LoadConfig() (Config, error) {
	conf := Config{
		Environment:        loadWithDefault("ENVIRONMENT", "development"),
		Domain:             loadWithDefault("DOMAIN", ""),
		NimbusStorageClass: loadWithDefault("NIMBUS_STORAGE_CLASS", ""),
		Database: Database{
			Host:     loadWithDefault("DB_HOST", ""),
			Port:     loadWithDefault("DB_PORT", "5432"),
			Name:     loadWithDefault("DB_NAME", ""),
			User:     loadWithDefault("DB_USER", ""),
			Password: loadWithDefault("DB_PASSWORD", ""),
		},
	}

	trans, found := uni.GetTranslator("en")
	if !found {
		return conf, errors.New("failed to find translator")
	}

	en_translations.RegisterDefaultTranslations(validate, trans)
	validate.RegisterTranslation("required", trans, func(ut ut.Translator) error {
		return ut.Add("required", "environment variable {0} is required", true)
	}, func(ut ut.Translator, fe validator.FieldError) string {
		t, _ := ut.T("required", toEnvName(fe.StructNamespace()))

		return t
	})
	validate.RegisterTranslation("port", trans, func(ut ut.Translator) error {
		return ut.Add("port", "{0}", true)
	}, func(ut ut.Translator, fe validator.FieldError) string {
		t, _ := ut.T("port", toEnvName(fe.StructNamespace()))
		return fmt.Sprintf("invalid %s (%s) - expected an int between 1 and 65535", t, fe.Value())
	})
	validate.RegisterTranslation("hostname_rfc1123", trans, func(ut ut.Translator) error {
		return ut.Add("hostname_rfc1123", "{0}", true)
	}, func(ut ut.Translator, fe validator.FieldError) string {
		t, _ := ut.T("hostname_rfc1123", toEnvName(fe.StructNamespace()))
		return fmt.Sprintf("invalid %s (%s) - expected a valid hostname", t, fe.Value())
	})
	validate.RegisterTranslation("oneof", trans, func(ut ut.Translator) error {
		return ut.Add("oneof", "{0}", true)
	}, func(ut ut.Translator, fe validator.FieldError) string {
		t, _ := ut.T("oneof", toEnvName(fe.StructNamespace()))
		return fmt.Sprintf("invalid %s (%s) - expected one of: %s", t, fe.Value(), fe.Param())
	})

	err := validate.Struct(conf)
	if err != nil {
		validationErrors := err.(validator.ValidationErrors)
		var msg strings.Builder
		for _, err := range validationErrors {
			msg.WriteString(err.Translate(trans))
			msg.WriteString("\n")
		}
		return conf, errors.New(msg.String())
	}

	return conf, nil
}

func toEnvName(field string) string {
	var sb strings.Builder
	field = strings.TrimPrefix(field, "Config.")

	field, found := strings.CutPrefix(field, "Database.")
	if found {
		sb.WriteString("DB_")
	}

	for idx := range len(field) {
		char := string(field[idx])
		if strings.ToUpper(char) == char && idx != 0 {
			_, _ = sb.WriteString("_")
		}
		_, _ = sb.WriteString(strings.ToUpper(char))
	}

	return sb.String()
}

func validatePort(fl validator.FieldLevel) bool {
	v, err := strconv.ParseUint(fl.Field().String(), 10, 16)
	return err == nil && v > 0
}
