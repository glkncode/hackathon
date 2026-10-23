package domain

import "errors"

var (
	ErrLeaseLost     = errors.New("ingestion lease lost")
	ErrInvalidState  = errors.New("invalid state")
	ErrAccessDenied  = errors.New("access denied")
	ErrNotFound      = errors.New("not found")
	ErrAlreadyExists = errors.New("already exists")
	ErrInvalidInput  = errors.New("invalid input")
)
