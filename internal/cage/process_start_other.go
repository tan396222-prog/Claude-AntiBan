//go:build !windows && !linux && !darwin

package cage

func processStartToken(int) string { return "" }
