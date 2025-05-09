// Code generated by sqlc. DO NOT EDIT.
// versions:
//   sqlc v1.28.0

package database

import (
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

type Project struct {
	ID     uuid.UUID
	Name   string
	ApiKey string
}

type Service struct {
	ID            uuid.UUID
	ProjectID     uuid.UUID
	ProjectBranch string
	ServiceName   string
	NodePorts     []int32
	Ingress       pgtype.Text
}

type Volume struct {
	Identifier    uuid.UUID
	VolumeName    string
	ProjectID     uuid.UUID
	ProjectBranch string
	Size          int32
}
