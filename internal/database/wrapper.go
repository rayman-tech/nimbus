package database

import (
	"sync"

	"context"
	"github.com/jackc/pgx/v5"
)

var queries *Queries
var connection *pgx.Conn
var once sync.Once

type Database struct {
	*Queries
	connection *pgx.Conn
}

func (db *Database) Close() error {
	if db == nil {
		return nil
	}

	return db.connection.Close(context.Background())
}
