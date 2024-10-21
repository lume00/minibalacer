package internal

import "log/slog"

func catchUnwind(fn func()) bool {
	panicked := false
	defer func() {
		if err := recover(); err != nil {
			panicked = true
			slog.Error("recovering from panic", "panic", err)
		}
	}()
	fn()
	return panicked
}
