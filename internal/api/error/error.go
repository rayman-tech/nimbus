// Package error provides standardized error handling for HTTP responses.
package error

import (
	"encoding/json"
	"fmt"
	"net/http"
)

type Error struct {
	Code    ErrorCode `json:"code"`
	Status  int       `json:"status"`
	Message string    `json:"message"`
	ErrorID string    `json:"error_id"`
}

func (e *Error) Error() string {
	data, _ := json.Marshal(e) //nolint:errchkjson
	return string(data)
}

func buildError(code ErrorCode, message, errorID string) Error {
	return Error{
		Code:    code,
		Status:  errorCodeToStatusCode[code],
		Message: message,
		ErrorID: errorID,
	}
}

func EncodeError(w http.ResponseWriter, code ErrorCode, message, errorID string) error {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(errorCodeToStatusCode[code])

	if err := json.NewEncoder(w).Encode(buildError(code, message, errorID)); err != nil {
		return fmt.Errorf("encoding error: %w", err)
	}
	return nil
}

func EncodeUnknownError(w http.ResponseWriter, message, errorID string, statusCode int) error {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	if err := json.NewEncoder(w).Encode(Error{
		Code:    UnknownError,
		Status:  statusCode,
		Message: message,
		ErrorID: errorID,
	}); err != nil {
		return fmt.Errorf("encoding error: %w", err)
	}
	return nil
}

func EncodeInternalError(w http.ResponseWriter, errorID string) error {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(errorCodeToStatusCode[InternalServerError])

	res := buildError(InternalServerError, "Internal Server Error", errorID)
	if err := json.NewEncoder(w).Encode(res); err != nil {
		return fmt.Errorf("encoding error: %w", err)
	}

	return nil
}
