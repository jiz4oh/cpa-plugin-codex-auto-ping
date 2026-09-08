package main

import "time"

const retryBaseDelay = 5 * time.Second

func init() {
	// Keep retry timing derived from maxAttempts instead of maintaining a
	// hand-written delay table. With maxAttempts=3 this produces 5s, 10s.
	retryDelays = make([]time.Duration, maxAttempts-1)
	for i := range retryDelays {
		retryDelays[i] = retryBaseDelay * time.Duration(1<<i)
	}
}
