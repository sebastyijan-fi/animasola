package store

import "errors"

var (
	ErrNotFound    = errors.New("not found")
	ErrPermission  = errors.New("permission denied")
	ErrRateLimited = errors.New("rate limited")
	ErrLimit       = errors.New("limit exceeded")
)
