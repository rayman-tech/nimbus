// Package environment provides a way to access environment variables

package env

import "os"

var Environment = os.Getenv("ENVIRONMENT")
var Home = os.Getenv("HOME")
var Domain = os.Getenv("DOMAIN")
var NimbusStorageClass = os.Getenv("NIMBUS_STORAGE_CLASS")
var DbUser = os.Getenv("DB_USER")
var DbPassword = os.Getenv("DB_PASSWORD")
var DbHost = os.Getenv("DB_HOST")
var DbPort = os.Getenv("DB_PORT")
var DbName = os.Getenv("DB_NAME")
