package llm

import (
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestClassifyHTTP(t *testing.T) {
	cases := []struct {
		name     string
		status   int
		header   http.Header
		sentinel error
		retry    time.Duration
	}{
		{"429 is rate limited", http.StatusTooManyRequests, nil, ErrRateLimited, 0},
		{"429 honours delta-seconds Retry-After", http.StatusTooManyRequests,
			http.Header{"Retry-After": []string{"30"}}, ErrRateLimited, 30 * time.Second},
		{"401 is unauthorized", http.StatusUnauthorized, nil, ErrUnauthorized, 0},
		{"403 is unauthorized", http.StatusForbidden, nil, ErrUnauthorized, 0},
		{"408 is unavailable", http.StatusRequestTimeout, nil, ErrUnavailable, 0},
		{"409 is unavailable", http.StatusConflict, nil, ErrUnavailable, 0},
		{"500 is unavailable", http.StatusInternalServerError, nil, ErrUnavailable, 0},
		{"503 is unavailable", http.StatusServiceUnavailable, nil, ErrUnavailable, 0},
		{"529 overloaded is unavailable", 529, nil, ErrUnavailable, 0},
		{"400 is bad request", http.StatusBadRequest, nil, ErrBadRequest, 0},
		{"404 is bad request", http.StatusNotFound, nil, ErrBadRequest, 0},
		{"422 is bad request", http.StatusUnprocessableEntity, nil, ErrBadRequest, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := tc.header
			if h == nil {
				h = http.Header{}
			}
			err := ClassifyHTTP("testprov", tc.status, h, []byte(`{"error":"x"}`))
			if !errors.Is(err, tc.sentinel) {
				t.Fatalf("errors.Is(%v, sentinel) = false for status %d", err, tc.status)
			}
			var api *APIError
			if !errors.As(err, &api) {
				t.Fatalf("errors.As APIError failed: %v", err)
			}
			if api.Provider != "testprov" || api.StatusCode != tc.status {
				t.Fatalf("APIError = %+v", api)
			}
			if api.RetryAfter != tc.retry {
				t.Fatalf("RetryAfter = %v, want %v", api.RetryAfter, tc.retry)
			}
			if !strings.Contains(err.Error(), "testprov") || !strings.Contains(err.Error(), `{"error":"x"}`) {
				t.Fatalf("Error() = %q", err.Error())
			}
		})
	}

	t.Run("HTTP-date Retry-After yields a positive duration", func(t *testing.T) {
		h := http.Header{"Retry-After": []string{time.Now().Add(90 * time.Second).UTC().Format(http.TimeFormat)}}
		err := ClassifyHTTP("p", http.StatusTooManyRequests, h, nil)
		var api *APIError
		if !errors.As(err, &api) {
			t.Fatal("no APIError")
		}
		if api.RetryAfter <= 0 || api.RetryAfter > 91*time.Second {
			t.Fatalf("RetryAfter = %v, want ~90s", api.RetryAfter)
		}
	})

	t.Run("past HTTP-date and garbage Retry-After yield zero", func(t *testing.T) {
		for _, v := range []string{
			time.Now().Add(-time.Minute).UTC().Format(http.TimeFormat),
			"soon",
			"-5",
		} {
			h := http.Header{"Retry-After": []string{v}}
			var api *APIError
			if !errors.As(ClassifyHTTP("p", 429, h, nil), &api) {
				t.Fatal("no APIError")
			}
			if api.RetryAfter != 0 {
				t.Fatalf("RetryAfter(%q) = %v, want 0", v, api.RetryAfter)
			}
		}
	})

	t.Run("body is truncated to ErrBodyLimit", func(t *testing.T) {
		big := strings.Repeat("x", ErrBodyLimit+500)
		var api *APIError
		if !errors.As(ClassifyHTTP("p", 500, http.Header{}, []byte(big)), &api) {
			t.Fatal("no APIError")
		}
		if len(api.Body) != ErrBodyLimit {
			t.Fatalf("len(Body) = %d, want %d", len(api.Body), ErrBodyLimit)
		}
	})
}

func TestDecodeFailure(t *testing.T) {
	base := errors.New("unexpected end of JSON input")

	t.Run("FinishLength wraps as ErrTruncated and keeps the decode error", func(t *testing.T) {
		err := DecodeFailure("p", FinishLength, base)
		if !errors.Is(err, ErrTruncated) {
			t.Fatalf("not ErrTruncated: %v", err)
		}
		if !errors.Is(err, base) {
			t.Fatalf("decode cause lost: %v", err)
		}
		if !strings.Contains(err.Error(), "WithMaxTokens") {
			t.Fatalf("remedy missing from message: %v", err)
		}
	})

	t.Run("FinishStop stays an untyped decode error", func(t *testing.T) {
		err := DecodeFailure("p", FinishStop, base)
		if errors.Is(err, ErrTruncated) {
			t.Fatalf("FinishStop must not classify as truncation: %v", err)
		}
		if !errors.Is(err, base) {
			t.Fatalf("decode cause lost: %v", err)
		}
	})
}
