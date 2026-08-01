//go:build unix

package service

import (
	"log/slog"
	"os"
	"os/signal"
	"syscall"
)

func installSignals(s *Service) {
	ch := make(chan os.Signal, 2)
	signal.Notify(ch, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)
	go func() {
		for sig := range ch {
			switch sig {
			case syscall.SIGTERM, syscall.SIGINT:
				slog.Info("signal received", "signal", sig.String())
				s.Stop()
			case syscall.SIGHUP:
				slog.Info("SIGHUP: reload requested")
				s.RequestReload()
			}
		}
	}()
}
