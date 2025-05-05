package util

import (
	"time"
)

// WithRetry executes a function with retries on failure.
func WithRetry(attempts int, sleep time.Duration, fn func() error) error {
	for i := 0; i < attempts; i++ {
		if err := fn(); err != nil {
			if i == attempts-1 {
				return err
			}
			time.Sleep(sleep * time.Duration(i+1))
			continue
		}
		return nil
	}
	return nil
}
