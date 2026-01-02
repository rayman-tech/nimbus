package database

import (
	"github.com/jackc/pgx/v5"
)

type Database struct {
	*Queries
	connection *pgx.Conn
}
