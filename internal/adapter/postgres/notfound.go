package postgres

import (
	"errors"

	"github.com/niazleushkin/kino/internal/domain/kernel"
)

func isNotFound(err error) bool { return errors.Is(err, kernel.ErrNotFound) }
