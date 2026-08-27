package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/spiffe/go-spiffe/v2/svid/jwtsvid"
)

func TestCredentialExpirationIsHalfwayToJWTExpiry(t *testing.T) {
	now := time.Date(2026, time.July, 18, 10, 0, 0, 0, time.UTC)
	jwtExpiry := now.Add(time.Hour)
	want := now.Add(30 * time.Minute)

	if got := credentialExpiration(now, jwtExpiry); !got.Equal(want) {
		t.Fatalf("credentialExpiration() = %s, want %s", got, want)
	}
}

func TestSelectSVIDByHint(t *testing.T) {
	// Named variables rather than a constructor helper, so the cases below can
	// assert which of two indistinguishable SVIDs was picked by pointer identity.
	one := &jwtsvid.SVID{Hint: "one"}
	two := &jwtsvid.SVID{Hint: "two"}
	three := &jwtsvid.SVID{Hint: "three"}
	upperOne := &jwtsvid.SVID{Hint: "One"}
	dupFirst := &jwtsvid.SVID{Hint: "dup"}
	dupSecond := &jwtsvid.SVID{Hint: "dup"}
	blankFirst := &jwtsvid.SVID{}
	blankSecond := &jwtsvid.SVID{}

	tests := []struct {
		name  string
		svids []*jwtsvid.SVID
		hint  string
		want  *jwtsvid.SVID
		// wantErrs lists substrings the error must contain. Empty means expect success.
		wantErrs []string
	}{
		{
			name:  "no hint returns the only SVID",
			svids: []*jwtsvid.SVID{blankFirst},
			want:  blankFirst,
		},
		{
			name:  "no hint returns the first of many",
			svids: []*jwtsvid.SVID{one, two},
			want:  one,
		},
		{
			name:  "hint matches the first SVID",
			svids: []*jwtsvid.SVID{one, two},
			hint:  "one",
			want:  one,
		},
		{
			name:  "hint matches a later SVID",
			svids: []*jwtsvid.SVID{one, two, three},
			hint:  "three",
			want:  three,
		},
		{
			name:  "duplicate hints return the first",
			svids: []*jwtsvid.SVID{dupFirst, dupSecond},
			hint:  "dup",
			want:  dupFirst,
		},
		{
			name:     "unmatched hint errors and lists the available hints",
			svids:    []*jwtsvid.SVID{one, two},
			hint:     "three",
			wantErrs: []string{`"three"`, `"one"`, `"two"`, "SPIFFE_JWT_HINT"},
		},
		{
			name:     "unmatched hint lists unhinted SVIDs as empty",
			svids:    []*jwtsvid.SVID{blankFirst, blankSecond},
			hint:     "one",
			wantErrs: []string{`"one"`, `"", ""`},
		},
		{
			name:     "hint matching is case sensitive",
			svids:    []*jwtsvid.SVID{upperOne},
			hint:     "one",
			wantErrs: []string{`"one"`, `"One"`},
		},
		{
			name:     "no SVIDs errors",
			svids:    nil,
			wantErrs: []string{"no JWT-SVIDs"},
		},
		{
			name:     "no SVIDs errors when a hint is requested",
			svids:    []*jwtsvid.SVID{},
			hint:     "one",
			wantErrs: []string{"no JWT-SVIDs"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := selectSVIDByHint(tt.svids, tt.hint)

			if len(tt.wantErrs) == 0 {
				if err != nil {
					t.Fatalf("selectSVIDByHint() returned unexpected error: %v", err)
				}
				if got != tt.want {
					t.Fatalf("selectSVIDByHint() = %+v, want %+v", got, tt.want)
				}
				return
			}

			if err == nil {
				t.Fatalf("selectSVIDByHint() = %+v, want an error", got)
			}
			if got != nil {
				t.Errorf("selectSVIDByHint() returned %+v alongside an error, want nil", got)
			}
			for _, want := range tt.wantErrs {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("selectSVIDByHint() error = %q, want it to contain %s", err, want)
				}
			}
		})
	}
}

func TestValidateExchangeEndpoint(t *testing.T) {
	tests := []struct {
		name     string
		endpoint string
		// wantErrs lists substrings the error must contain. Empty means expect success.
		wantErrs []string
	}{
		{
			name:     "https endpoint",
			endpoint: "https://exchange.example.org/token",
		},
		{
			name:     "http is rejected",
			endpoint: "http://exchange.example.org/token",
			wantErrs: []string{"must be an https:// URL"},
		},
		{
			name:     "no scheme is rejected",
			endpoint: "exchange.example.org/token",
			wantErrs: []string{"must be an https:// URL"},
		},
		{
			name:     "no host is rejected",
			endpoint: "https:///token",
			wantErrs: []string{"has no host"},
		},
		{
			name:     "unparseable url is rejected",
			endpoint: "https://exchange.example.org/token\x7f",
			wantErrs: []string{"invalid SPIFFE_JWT_EXCHANGE_ENDPOINT"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateExchangeEndpoint(tt.endpoint)

			if len(tt.wantErrs) == 0 {
				if err != nil {
					t.Fatalf("validateExchangeEndpoint(%q) returned unexpected error: %v", tt.endpoint, err)
				}
				return
			}
			if err == nil {
				t.Fatalf("validateExchangeEndpoint(%q) = nil, want an error", tt.endpoint)
			}
			for _, want := range tt.wantErrs {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("validateExchangeEndpoint(%q) error = %q, want it to contain %q", tt.endpoint, err, want)
				}
			}
		})
	}
}

// TestExchangeTokenRequest pins the RFC 8693 wire format against renames, and
// that audience is sent only when asked for — an exchange that derives it from
// the subject token rejects a request that carries one.
func TestExchangeTokenRequest(t *testing.T) {
	tests := []struct {
		name     string
		audience string
		// expiresIn differs per case so the expiry cannot come from a constant.
		expiresIn int64
		// wantAudience is the value the exchange must receive. Empty means the
		// parameter must be absent, not present and empty.
		wantAudience []string
	}{
		{
			name:      "no audience requested",
			expiresIn: 600,
		},
		{
			expiresIn:    1800,
			name:         "audience requested",
			audience:     "//iam.googleapis.com/projects/1/locations/global/workloadIdentityPools/p/providers/v",
			wantAudience: []string{"//iam.googleapis.com/projects/1/locations/global/workloadIdentityPools/p/providers/v"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if err := r.ParseForm(); err != nil {
					t.Errorf("ParseForm() returned unexpected error: %v", err)
				}
				if got := r.Header.Get("Content-Type"); got != "application/x-www-form-urlencoded" {
					t.Errorf("Content-Type = %q, want %q", got, "application/x-www-form-urlencoded")
				}
				if want := "urn:ietf:params:oauth:grant-type:token-exchange"; r.PostForm.Get("grant_type") != want {
					t.Errorf("grant_type = %q, want %q", r.PostForm.Get("grant_type"), want)
				}
				if got := r.PostForm.Get("subject_token"); got != "subject-jwt" {
					t.Errorf("subject_token = %q, want %q", got, "subject-jwt")
				}
				if want := "urn:ietf:params:oauth:token-type:jwt"; r.PostForm.Get("subject_token_type") != want {
					t.Errorf("subject_token_type = %q, want %q", r.PostForm.Get("subject_token_type"), want)
				}
				if got := r.PostForm["audience"]; !slices.Equal(got, tt.wantAudience) {
					t.Errorf("audience = %q, want %q", got, tt.wantAudience)
				}
				w.Header().Set("Content-Type", "application/json")
				body := fmt.Sprintf(`{"access_token":"exchanged","issued_token_type":"urn:ietf:params:oauth:token-type:jwt","token_type":"Bearer","expires_in":%d}`, tt.expiresIn)
				if _, err := w.Write([]byte(body)); err != nil {
					t.Errorf("Write() returned unexpected error: %v", err)
				}
			}))
			defer server.Close()

			before := time.Now()
			token, expiry, err := exchangeToken(context.Background(), server.URL, "subject-jwt", tt.audience)
			if err != nil {
				t.Fatalf("exchangeToken() returned unexpected error: %v", err)
			}

			if token != "exchanged" {
				t.Errorf("exchangeToken() token = %q, want %q", token, "exchanged")
			}
			if want := before.Add(time.Duration(tt.expiresIn) * time.Second); expiry.Before(want) || expiry.After(want.Add(time.Minute)) {
				t.Errorf("exchangeToken() expiry = %s, want about %s", expiry, want)
			}
		})
	}
}

// TestExchangeTokenRefusesRedirect covers what validateExchangeEndpoint cannot:
// net/http replays a POST body on a 307, which could reach a plaintext host.
func TestExchangeTokenRefusesRedirect(t *testing.T) {
	var redirected atomic.Bool
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		redirected.Store(true)
	}))
	defer target.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer server.Close()

	token, _, err := exchangeToken(context.Background(), server.URL, "subject-jwt", "")
	if err == nil {
		t.Fatalf("exchangeToken() = %q, want an error", token)
	}
	if !strings.Contains(err.Error(), "must not redirect") {
		t.Errorf("exchangeToken() error = %q, want it to name the redirect", err)
	}
	if redirected.Load() {
		t.Error("exchangeToken() followed the redirect, sending the subject token to the target")
	}
}

func TestExchangeTokenErrors(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
		// wantErrs lists substrings the error must contain.
		wantErrs []string
	}{
		{
			name:     "rfc 6749 error with a description",
			status:   http.StatusBadRequest,
			body:     `{"error":"invalid_grant","error_description":"subject token is expired"}`,
			wantErrs: []string{`"invalid_grant": "subject token is expired" (HTTP 400)`},
		},
		{
			name:     "rfc 6749 error without a description",
			status:   http.StatusForbidden,
			body:     `{"error":"unauthorized_client"}`,
			wantErrs: []string{`"unauthorized_client" (HTTP 403)`},
		},
		{
			name:     "non-json error body is reported with its status, trimmed",
			status:   http.StatusBadGateway,
			body:     "  upstream unavailable\n",
			wantErrs: []string{`HTTP 502: "upstream unavailable"`},
		},
		{
			name:     "unparseable success body keeps the evidence",
			status:   http.StatusOK,
			body:     "<html>captive portal</html>",
			wantErrs: []string{"parsing the exchange response", "captive portal"},
		},
		{
			name:     "success without an access token",
			status:   http.StatusOK,
			body:     `{"expires_in":600}`,
			wantErrs: []string{"no access_token"},
		},
		{
			// DEL, the upper end of the range: printable ASCII stops at 0x7e.
			name:     "access token with a DEL character",
			status:   http.StatusOK,
			body:     "{\"access_token\":\"tok\\u007f\",\"expires_in\":600}",
			wantErrs: []string{"outside printable ASCII"},
		},
		{
			name:     "access token with a control character",
			status:   http.StatusOK,
			body:     "{\"access_token\":\"line\\nbreak\",\"expires_in\":600}",
			wantErrs: []string{"outside printable ASCII"},
		},
		{
			name:     "success without expires_in",
			status:   http.StatusOK,
			body:     `{"access_token":"exchanged","token_type":"Bearer"}`,
			wantErrs: []string{"no usable expires_in"},
		},
		{
			name:     "negative expires_in",
			status:   http.StatusOK,
			body:     `{"access_token":"exchanged","expires_in":-1}`,
			wantErrs: []string{"no usable expires_in"},
		},
		{
			// math.MaxInt32 + 1, so time.Duration(n) * time.Second cannot overflow.
			name:     "expires_in above the overflow guard",
			status:   http.StatusOK,
			body:     `{"access_token":"exchanged","expires_in":2147483648}`,
			wantErrs: []string{"no usable expires_in"},
		},
		{
			// One byte over the limit, so this pins where quoteForTerminal cuts.
			name:     "an oversized error body is truncated at 256 bytes",
			status:   http.StatusBadGateway,
			body:     strings.Repeat("a", 257),
			wantErrs: []string{`HTTP 502: "` + strings.Repeat("a", 256) + `..."`},
		},
		{
			// JSON but not the RFC 6749 shape: without the e.Error guard, an empty code.
			name:     "json error body without an error member keeps the body",
			status:   http.StatusBadGateway,
			body:     `{"message":"upstream unavailable"}`,
			wantErrs: []string{`HTTP 502: "{\"message\":\"upstream unavailable\"}"`},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.status)
				if _, err := w.Write([]byte(tt.body)); err != nil {
					t.Errorf("Write() returned unexpected error: %v", err)
				}
			}))
			defer server.Close()

			token, _, err := exchangeToken(context.Background(), server.URL, "subject-jwt", "")

			if err == nil {
				t.Fatalf("exchangeToken() = %q, want an error", token)
			}
			if token != "" {
				t.Errorf("exchangeToken() returned token %q alongside an error, want no token", token)
			}
			for _, want := range tt.wantErrs {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("exchangeToken() error = %q, want it to contain %q", err, want)
				}
			}
		})
	}
}

// TestExchangeTokenDoesNotEchoTokenBearingBody covers the one response that must
// not reach stderr: a 200 whose expires_in is a JSON string aborts the decode
// with the issued token still in the body, and client-go inherits our stderr.
func TestExchangeTokenDoesNotEchoTokenBearingBody(t *testing.T) {
	const issued = "the-issued-token"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := w.Write([]byte(`{"access_token":"` + issued + `","token_type":"Bearer","expires_in":"600"}`)); err != nil {
			t.Errorf("Write() returned unexpected error: %v", err)
		}
	}))
	defer server.Close()

	_, _, err := exchangeToken(context.Background(), server.URL, "subject-jwt", "")
	if err == nil {
		t.Fatal("exchangeToken() = nil error, want a decode error")
	}
	if strings.Contains(err.Error(), issued) {
		t.Errorf("exchangeToken() error = %q, want it not to contain the issued token", err)
	}
	// The operator still needs to know which member did not decode.
	if !strings.Contains(err.Error(), "expires_in") {
		t.Errorf("exchangeToken() error = %q, want it to name the member that failed", err)
	}
}

// TestExchangeTokenHonoursContext covers -timeout reaching the exchange.
func TestExchangeTokenHonoursContext(t *testing.T) {
	// The handler blocks until the test releases it, not until the request context
	// is done: cancelling the client side does not reliably unblock the server
	// side, and Server.Close waits for handlers. Released before the close.
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
	}))
	defer server.Close()
	defer close(release)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	errs := make(chan error, 1)
	go func() {
		_, _, err := exchangeToken(ctx, server.URL, "subject-jwt", "")
		errs <- err
	}()

	select {
	case err := <-errs:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("exchangeToken() error = %v, want %v", err, context.DeadlineExceeded)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("exchangeToken() did not return once the context deadline passed")
	}
}
