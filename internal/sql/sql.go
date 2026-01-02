// Package sql includes the database schema
package sql

import _ "embed"

//go:embed schema.sql
var schema string

func Schema() string {
	return schema
}
