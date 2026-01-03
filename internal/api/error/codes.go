package error

import "net/http"

type ErrorCode string

const (
	UnknownError            ErrorCode = "unknown_error"
	InternalServerError     ErrorCode = "internal_server_error"
	BadRequest              ErrorCode = "bad_request"
	UnprocessibleContent    ErrorCode = "unprocessible_entity"
	InvalidCredentials      ErrorCode = "invalid_credentials"
	InsufficientPermissions ErrorCode = "insufficient_permissions"
	InvalidAPIKey           ErrorCode = "invalid_api_key"
	ProjectNotFound         ErrorCode = "project_not_found"
	DisabledBranchPreview   ErrorCode = "disabled_branch_preview"
	ServiceNotFound         ErrorCode = "service_not_found"
)

var errorCodeToStatusCode = map[ErrorCode]int{
	UnknownError:            0, // No error code - unknown
	InternalServerError:     http.StatusInternalServerError,
	BadRequest:              http.StatusBadRequest,
	UnprocessibleContent:    http.StatusUnprocessableEntity,
	InsufficientPermissions: http.StatusForbidden,
	InvalidCredentials:      http.StatusUnauthorized,
	InvalidAPIKey:           http.StatusUnauthorized,
	ProjectNotFound:         http.StatusNotFound,
	DisabledBranchPreview:   http.StatusConflict,
	ServiceNotFound:         http.StatusNotFound,
}

func (ec ErrorCode) Status() int {
	return errorCodeToStatusCode[ec]
}

func (ec ErrorCode) String() string {
	return string(ec)
}
