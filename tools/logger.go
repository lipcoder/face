package tools

import "log/slog"

type Logger struct {
	logger *slog.Logger
}

func NewLogger(logger *slog.Logger) *Logger {
	if logger == nil {
		logger = slog.Default()
	}

	return &Logger{
		logger: logger,
	}
}

func (l *Logger) Info(msg string, args ...any) {
	l.logger.Info(msg, args...)
}

func (l *Logger) Error(msg string, args ...any) {
	l.logger.Error(msg, args...)
}

func (l *Logger) Run() {
	l.logger.Info("log run")
}