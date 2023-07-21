package logger

import "golang.org/x/exp/slog"

var Log *slog.Logger = slog.Default()

func Error(msg string, args ...interface{}) {
	Log.Error(msg, args...)
}
