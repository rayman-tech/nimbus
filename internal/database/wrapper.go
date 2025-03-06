package database

import (
	"context"
	"fmt"
	"os"
	"sync"

	"github.com/jackc/pgx/v5"
)

var queries *Queries
var connection *pgx.Conn
var once sync.Once

func GetQueries() *Queries {
	once.Do(func() {
		ctx := context.Background()

		conn, err := pgx.Connect(ctx,
			fmt.Sprintf(
				"user=%s password=%s host=%s port=%s dbname=%s sslmode=disable",
				os.Getenv("DB_USER"),
				os.Getenv("DB_PASSWORD"),
				os.Getenv("DB_HOST"),
				os.Getenv("DB_PORT"),
				os.Getenv("DB_NAME")))
		if err != nil {
			panic(err)
		}

		connection = conn
		queries = New(conn)
	})

	return queries
}

func Close() {
	if connection != nil {
		connection.Close(context.Background())
	}
}
