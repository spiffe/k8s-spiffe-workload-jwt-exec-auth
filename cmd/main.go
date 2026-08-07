package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/spiffe/go-spiffe/v2/svid/jwtsvid"
	"github.com/spiffe/go-spiffe/v2/workloadapi"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func main() {
	// client-go inherits this process's stderr and reports only "failed with exit
	// code N", so anything logged here is the operator's entire diagnostic. Drop
	// log's timestamp and name the plugin instead.
	log.SetFlags(0)
	log.SetPrefix("k8s-spiffe-workload-jwt-exec-auth: ")

	timeout := flag.Duration("timeout", 0,
		"max time to wait for the JWT-SVID from the workload API socket (e.g. 5s). 0 = wait forever")
	flag.Parse()

	socketPath, ok := os.LookupEnv("SPIFFE_ENDPOINT_SOCKET")
	if !ok {
		socketPath = "unix:///tmp/spire-agent/public/api.sock"
	}

	audience, ok := os.LookupEnv("SPIFFE_JWT_AUDIENCE")
	if !ok {
		audience = "k8s"
	}

	// Unset or empty means "no preference": use the first JWT-SVID the Workload API
	// returns, which is what this plugin has always done. Set this when the Workload
	// API returns several JWT-SVIDs (for example a SPIRE HA broker fronting multiple
	// entry-scoped SVIDs) to pin a specific one.
	hint := os.Getenv("SPIFFE_JWT_HINT")

	execCredentialVersion, ok := os.LookupEnv("EXEC_CREDENTIAL_VERSION")
	if !ok {
		execCredentialVersion = "v1"
	}

	ctx := context.Background()
	if *timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, *timeout)
		defer cancel()
	}

	jwtSource, err := workloadapi.NewJWTSource(
		ctx,
		workloadapi.WithClientOptions(workloadapi.WithAddr(socketPath)),
	)
	if err != nil {
		log.Fatal(err)

	}
	svids, err := jwtSource.FetchJWTSVIDs(ctx, jwtsvid.Params{
		Audience: audience,
	})
	if err != nil {
		log.Fatal(err)
	}
	// Everything that can fail must fail before the first write to stdout below:
	// client-go decodes stdout as an ExecCredential, so a half-written document
	// would surface as a confusing decode error instead of the real problem.
	svid, err := selectSVIDByHint(svids, hint)
	if err != nil {
		log.Fatal(err)
	}

	now := time.Now()
	expiry, err := metav1.NewTime(credentialExpiration(now, svid.Expiry)).MarshalJSON()
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("apiVersion: client.authentication.k8s.io/%s\n", execCredentialVersion)
	fmt.Print("kind: ExecCredential\n")
	fmt.Print("spec:\n")
	fmt.Print(" interactive: false\n")
	fmt.Print("status:\n")
	fmt.Printf("  expirationTimestamp: %s\n", string(expiry))
	fmt.Printf("  token: %s\n", svid.Marshal())
}

func credentialExpiration(now, jwtExpiry time.Time) time.Time {
	return now.Add(jwtExpiry.Sub(now) / 2)
}

// selectSVIDByHint returns the JWT-SVID whose hint matches the requested hint. An
// empty hint means no preference, in which case the first JWT-SVID is returned. If
// a hint is requested but nothing matches, an error naming the available hints is
// returned rather than a JWT-SVID for a different identity.
func selectSVIDByHint(svids []*jwtsvid.SVID, hint string) (*jwtsvid.SVID, error) {
	if len(svids) == 0 {
		return nil, errors.New("the workload API returned no JWT-SVIDs")
	}
	if hint == "" {
		return svids[0], nil
	}

	available := make([]string, 0, len(svids))
	for _, svid := range svids {
		if svid.Hint == hint {
			return svid, nil
		}
		available = append(available, fmt.Sprintf("%q", svid.Hint))
	}

	return nil, fmt.Errorf("no JWT-SVID with hint %q (SPIFFE_JWT_HINT); available hints: %s",
		hint, strings.Join(available, ", "))
}
