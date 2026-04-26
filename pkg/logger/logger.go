package logger

import (
	"os"
	"time"

	"github.com/rs/zerolog"
)

// New returns a zerolog logger with a stable field set.
// service is emitted on every line so logs from all services can be filtered.
func New(service, env string) zerolog.Logger {
	zerolog.TimeFieldFormat = time.RFC3339Nano

	level := zerolog.InfoLevel
	if env == "local" || env == "dev" {
		level = zerolog.DebugLevel
	}

	return zerolog.New(os.Stdout).
		Level(level).
		With().
		Timestamp().
		Str("service", service).
		Str("env", env).
		Logger()
}
