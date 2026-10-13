package model

import (
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const completionAttempts = 3

func retryableStatus(status int) bool {
	switch status {
	case 429, 500, 502, 503, 504:
		return true
	}
	return false
}

func retryDelay(status int, header string, attempt int, now time.Time) time.Duration {
	// Respect server advice, including explicit zero, without allowing duration overflow.
	if seconds, err := strconv.ParseInt(strings.TrimSpace(header), 10, 64); err == nil && seconds >= 0 {
		if seconds > int64((24*time.Hour)/time.Second) {
			return 24 * time.Hour
		}
		return time.Duration(seconds) * time.Second
	}
	if date, err := http.ParseTime(header); err == nil {
		return max(0, date.Sub(now))
	}
	base := time.Second
	if status == http.StatusTooManyRequests {
		base = 30 * time.Second
	}
	return base*time.Duration(1<<attempt) + time.Duration(rand.Int64N(int64(250*time.Millisecond)))
}

// Retry only explicit transient HTTP responses. Transport errors may leave
// request execution ambiguous and are returned unchanged. Each attempt uses
// the original payload and shares its overall deadline.
func doCompletionRequest(client *http.Client, req *http.Request, onRetry func(int, time.Duration), onRemaining func(int64)) (*http.Response, error) {
	for attempt := 0; attempt < completionAttempts; attempt++ {
		current := req.Clone(req.Context())
		body, err := req.GetBody()
		if err != nil {
			return nil, err
		}
		current.Body = body
		resp, err := client.Do(current)
		if err != nil {
			return nil, err
		}
		if onRemaining != nil {
			if remaining := rateLimitRemaining(resp.Header); remaining != nil {
				onRemaining(*remaining)
			}
		}
		if !retryableStatus(resp.StatusCode) || attempt == completionAttempts-1 {
			return resp, nil
		}
		delay := retryDelay(resp.StatusCode, resp.Header.Get("Retry-After"), attempt, time.Now())
		if deadline, ok := req.Context().Deadline(); ok && delay >= time.Until(deadline) {
			return resp, nil
		}
		// Limit draining for connection reuse; close before waiting for another attempt.
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
		resp.Body.Close()
		if onRetry != nil {
			onRetry(resp.StatusCode, delay)
		}
		timer := time.NewTimer(delay)
		select {
		case <-req.Context().Done():
			timer.Stop()
			return nil, fmt.Errorf("completion retry stopped: %w", req.Context().Err())
		case <-timer.C:
		}
	}
	panic("unreachable")
}

// Missing or invalid values stay unknown rather than becoming a zero quota.
func rateLimitRemaining(header http.Header) *int64 {
	value := strings.TrimSpace(header.Get("X-RateLimit-Remaining"))
	if value == "" {
		return nil
	}
	for _, c := range value {
		if c < '0' || c > '9' {
			return nil
		}
	}
	remaining, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return nil
	}
	return &remaining
}
