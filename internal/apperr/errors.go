package apperr

import (
	"errors"
	"fmt"
)

type Code string

const (
	CodeValidation   Code = "validation_failed"
	CodeUnauthorized Code = "unauthorized"
	CodeForbidden    Code = "forbidden"
	CodeNotFound     Code = "not_found"
	CodeConflict     Code = "conflict"
	CodeExpired      Code = "expired"
	CodeUnavailable  Code = "unavailable"
	CodeInternal     Code = "internal_error"
)

type Error struct {
	Code      Code
	Message   string
	Operation string
	Cause     error
	Details   map[string]string
}

func (e *Error) Error() string {
	if e.Operation == "" {
		return e.Message
	}
	return e.Operation + ": " + e.Message
}

func (e *Error) Unwrap() error { return e.Cause }

func New(code Code, message string) *Error {
	return &Error{Code: code, Message: message}
}

func Wrap(code Code, operation, message string, cause error) *Error {
	return &Error{Code: code, Operation: operation, Message: message, Cause: cause}
}

func WithDetail(err *Error, key, value string) *Error {
	copy := *err
	copy.Details = cloneDetails(err.Details)
	copy.Details[key] = value
	return &copy
}

func cloneDetails(source map[string]string) map[string]string {
	result := make(map[string]string, len(source)+1)
	for key, value := range source {
		result[key] = value
	}
	return result
}

func As(err error) (*Error, bool) {
	var target *Error
	if errors.As(err, &target) {
		return target, true
	}
	return nil, false
}

func IsCode(err error, code Code) bool {
	target, ok := As(err)
	return ok && target.Code == code
}

func Validation(field, reason string) *Error {
	return WithDetail(New(CodeValidation, fmt.Sprintf("invalid %s", field)), field, reason)
}

func NotFound(entity, id string) *Error {
	return WithDetail(New(CodeNotFound, entity+" was not found"), "id", id)
}

func Conflict(message string) *Error { return New(CodeConflict, message) }

func Internal(operation string, cause error) *Error {
	return Wrap(CodeInternal, operation, "the operation could not be completed", cause)
}
