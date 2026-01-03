package error

import "net/http"

type ErrorCode string

const (
	UnknownError            ErrorCode = "unknown_error"
	InternalServerError     ErrorCode = "internal_server_error"
	BadRequest              ErrorCode = "bad_request"
	UnprocessibleEntity     ErrorCode = "unprocessible_entity"
	InvalidCredentials      ErrorCode = "invalid_credentials"
	InsufficientPermissions ErrorCode = "insufficient_permissions"
)

var errorCodeToStatusCode = map[ErrorCode]int{
	UnknownError:            0, // No error code - unknown
	InternalServerError:     http.StatusInternalServerError,
	BadRequest:              http.StatusBadRequest,
	UnprocessibleEntity:     http.StatusUnprocessableEntity,
	InsufficientPermissions: http.StatusForbidden,
	InvalidCredentials:      http.StatusUnauthorized,
}

func (ec ErrorCode) StatusCode() int {
	return errorCodeToStatusCode[ec]
}

func (ec ErrorCode) String() string {
	return string(ec)
}
