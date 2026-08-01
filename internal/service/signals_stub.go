//go:build !unix

package service

func installSignals(s *Service) {
	_ = s
}
