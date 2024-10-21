package internal

import (
	"log"
	"log/slog"
)

type Logger struct {
	EnableDebug   bool        `json:"enableDebug"`
	ch            chan []byte `json:"-"`
	DefaultLogger log.Logger  `json:"-"`
}

func (logger *Logger) Start() error {

	//copying original logger
	logger.DefaultLogger = *log.Default()

	var slogHandler *slog.HandlerOptions = nil
	if logger.EnableDebug {
		slogHandler = &slog.HandlerOptions{Level: slog.LevelDebug}
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(logger, slogHandler)))

	//setting default this as default logger for log
	log.SetOutput(logger)

	//init channel for async logging
	logger.ch = make(chan []byte)
	go logger.logRecv()

	return nil
}

func (logger *Logger) Stop() {
	log.SetOutput(logger.DefaultLogger.Writer())
	close(logger.ch)
}

func (logger *Logger) logRecv() {
	for message := range logger.ch {
		msgstring := string(message)
		logger.DefaultLogger.Print(msgstring)
	}
}

func (logger *Logger) Write(p []byte) (n int, err error) {
	l := len(p)

	catchUnwind(func() {
		logger.ch <- p
	})

	return l, nil
}
