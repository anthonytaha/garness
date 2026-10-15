package model

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestCompletionResponseFormat(t *testing.T) {
	for _, format := range []map[string]any{nil, {"type": "json_schema", "json_schema": map[string]any{"name": "fixture", "strict": true, "schema": map[string]any{"type": "object"}}}} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Error(err)
			}
			if actual, ok := payload["response_format"]; format == nil && ok || format != nil && !reflect.DeepEqual(actual, format) {
				t.Error("response format not preserved", actual)
			}
			io.WriteString(w, `{"choices":[{"message":{"content":"{}"}}]}`)
		}))
		_, err := Chat(Provider{BaseUrl: server.URL}, nil, &CompleteOptions{ResponseFormat: format})
		server.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestCompletionRetries(t *testing.T) {
	for _, test := range []struct {
		name      string
		status    int
		failures  int
		wantCalls int
		wantError bool
	}{
		{"recovers from unavailable", 503, 2, 3, false}, {"bounded unavailable", 503, 9, 3, true}, {"rate limited", 429, 1, 2, false}, {"persistent rate limit", 429, 9, 3, true}, {"bad request", 400, 9, 1, true}, {"unauthorized", 401, 9, 1, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			var original string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				b, _ := io.ReadAll(r.Body)
				if calls == 1 {
					original = string(b)
				} else if string(b) != original {
					t.Error("retry changed payload")
				}
				if r.Header.Get("Authorization") != "Bearer fixture" {
					t.Error("missing auth")
				}
				w.Header().Set("Content-Type", "application/json")
				if calls <= test.failures {
					w.Header().Set("Retry-After", "0")
					w.WriteHeader(test.status)
					io.WriteString(w, `{"message":"Service unavailable."}`)
					return
				}
				json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": "recovered"}}}})
			}))
			defer server.Close()
			resp, e := Chat(Provider{BaseUrl: server.URL, Model: "fixture", ApiKey: "fixture"}, []map[string]any{{"role": "user", "content": "hello"}}, nil)
			if (e != nil) != test.wantError || calls != test.wantCalls {
				t.Fatalf("calls=%d error=%v", calls, e)
			}
			if e != nil && test.status == 429 && !strings.Contains(e.Error(), "rate limit exceeded") {
				t.Fatalf("unclear rate limit: %v", e)
			}
			if e == nil && resp.Content != "recovered" {
				t.Fatal("unexpected completion")
			}
		})
	}
}

func TestRetryDelay(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	if retryDelay(503, "2", 0, now) != 2*time.Second {
		t.Fatal("seconds ignored")
	}
	if retryDelay(503, now.Add(5*time.Second).Format(http.TimeFormat), 0, now) != 5*time.Second {
		t.Fatal("date ignored")
	}
	if delay := retryDelay(503, "invalid", 1, now); delay < 2*time.Second || delay >= 2250*time.Millisecond {
		t.Fatal("invalid backoff")
	}
}

func TestRetryHonorsDeadline(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Retry-After", "99999")
		w.WriteHeader(503)
		io.WriteString(w, `{"message":"unavailable"}`)
	}))
	defer server.Close()
	_, e := Chat(Provider{BaseUrl: server.URL}, nil, &CompleteOptions{Timeout: time.Second})
	if e == nil || !strings.Contains(e.Error(), "503") || calls != 1 {
		t.Fatalf("calls=%d error=%v", calls, e)
	}
}

func TestRateLimitBackoff(t *testing.T) {
	now := time.Now()
	for attempt := 0; attempt < 2; attempt++ {
		delay := retryDelay(429, "", attempt, now)
		base := 30 * time.Second * time.Duration(1<<attempt)
		if delay < base || delay >= base+250*time.Millisecond {
			t.Fatalf("rate limit delay=%s", delay)
		}
	}
	if retryDelay(429, "2", 0, now) != 2*time.Second {
		t.Fatal("ignored server retry advice")
	}
}
func TestRetryNotification(t *testing.T) {
	calls, notifications := 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(429)
			io.WriteString(w, `{"message":"rate limited"}`)
			return
		}
		io.WriteString(w, `{"choices":[{"message":{"content":"done"}}]}`)
	}))
	defer server.Close()
	_, err := Chat(Provider{BaseUrl: server.URL}, nil, &CompleteOptions{OnRetry: func(status int, delay time.Duration) {
		notifications++
		if status != 429 || delay != 0 {
			t.Errorf("status=%d delay=%s", status, delay)
		}
	}})
	if err != nil || notifications != 1 {
		t.Fatalf("notifications=%d error=%v", notifications, err)
	}
}

func TestRateLimitRemaining(t *testing.T) {
	for _, test := range []struct {
		header string
		want   *int64
	}{
		{"", nil}, {"not-a-number", nil}, {"-1", nil}, {"+1", nil}, {"9223372036854775808", nil}, {"0", ptrCount(0)}, {" 42 ", ptrCount(42)},
	} {
		t.Run(test.header, func(t *testing.T) {
			notifications := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if test.header != "" {
					w.Header().Set("X-RateLimit-Remaining", test.header)
				}
				io.WriteString(w, `{"choices":[{"message":{"content":"done"}}]}`)
			}))
			defer server.Close()
			resp, err := Chat(Provider{BaseUrl: server.URL}, nil, &CompleteOptions{OnRateLimitRemaining: func(n int64) {
				notifications++
				if test.want == nil || n != *test.want {
					t.Errorf("unexpected quota %d", n)
				}
			}})
			if err != nil {
				t.Fatal(err)
			}
			if test.want == nil {
				if resp.RateLimitRemaining != nil || notifications != 0 {
					t.Fatal("unknown quota became a number")
				}
			} else if resp.RateLimitRemaining == nil || *resp.RateLimitRemaining != *test.want || notifications != 1 {
				t.Fatal("missing reported quota")
			}
		})
	}
}
func ptrCount(n int64) *int64 { return &n }

func TestRateLimitRemainingAcrossRetries(t *testing.T) {
	calls := 0
	var reported []int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("X-RateLimit-Remaining", "0")
		if calls == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(429)
			return
		}
		w.Header().Set("X-RateLimit-Remaining", "10")
		io.WriteString(w, `{"choices":[{"message":{"content":"done"}}]}`)
	}))
	defer server.Close()
	resp, err := Chat(Provider{BaseUrl: server.URL}, nil, &CompleteOptions{OnRateLimitRemaining: func(n int64) { reported = append(reported, n) }})
	if err != nil || len(reported) != 2 || reported[0] != 0 || reported[1] != 10 || resp.RateLimitRemaining == nil || *resp.RateLimitRemaining != 10 {
		t.Fatalf("reported=%v response=%v error=%v", reported, resp, err)
	}
}

func TestMaxOutputTokens(t *testing.T) {
	for _, limit := range []int{0, 4096} {
		t.Run(fmt.Sprint(limit), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var payload map[string]any
				json.NewDecoder(r.Body).Decode(&payload)
				want := limit
				if want == 0 {
					want = DefaultMaxOutputTokens
				}
				if payload["max_tokens"] != float64(want) {
					t.Errorf("max_tokens=%v", payload["max_tokens"])
				}
				io.WriteString(w, `{"choices":[{"message":{"content":"done"}}]}`)
			}))
			defer server.Close()
			if _, err := Chat(Provider{BaseUrl: server.URL}, nil, &CompleteOptions{MaxTokens: limit}); err != nil {
				t.Fatal(err)
			}
		})
	}
}
