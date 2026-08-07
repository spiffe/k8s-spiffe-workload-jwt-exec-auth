package main

import (
	"strings"
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
