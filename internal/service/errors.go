package service

import "errors"

var (
	ErrNotFound        = errors.New("resource not found")
	ErrConflict        = errors.New("conflict")
	ErrUpstreamFailure = errors.New("upstream failure")
	ErrUpstreamTimeout = errors.New("upstream timeout")
)
