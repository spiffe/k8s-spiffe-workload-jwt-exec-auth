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

	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"github.com/spiffe/go-spiffe/v2/svid/jwtsvid"
	"github.com/spiffe/go-spiffe/v2/workloadapi"
	svidv1 "github.com/spiffe/spire-api-sdk/proto/spire/api/server/svid/v1"
	apitypes "github.com/spiffe/spire-api-sdk/proto/spire/api/types"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// JWT-SVID sources selectable via SPIFFE_JWT_SOURCE.
const (
	// sourceWorkloadAPI fetches the JWT-SVID of the calling workload from the
	// SPIFFE Workload API socket. This is the default.
	sourceWorkloadAPI = "workload-api"
	// sourceServerAdminAPI mints a JWT-SVID for a configured SPIFFE ID directly
	// from the SPIRE Server admin API socket. Useful where the Workload API is
	// not reachable but the server admin socket is (e.g. a spire-server
	// sidecar), and where depending on agent attestation is undesirable.
	sourceServerAdminAPI = "server-admin-api"
)

func main() {
	// client-go inherits this process's stderr and reports only "failed with exit
	// code N", so anything logged here is the operator's entire diagnostic. Drop
	// log's timestamp and name the plugin instead.
	log.SetFlags(0)
	log.SetPrefix("k8s-spiffe-workload-jwt-exec-auth: ")

	timeout := flag.Duration("timeout", 0,
		"max time to wait for the JWT-SVID (e.g. 5s). 0 = wait forever")
	flag.Parse()

	audience, ok := os.LookupEnv("SPIFFE_JWT_AUDIENCE")
	if !ok {
		audience = "k8s"
	}

	execCredentialVersion, ok := os.LookupEnv("EXEC_CREDENTIAL_VERSION")
	if !ok {
		execCredentialVersion = "v1"
	}

	source, ok := os.LookupEnv("SPIFFE_JWT_SOURCE")
	if !ok {
		source = sourceWorkloadAPI
	}

	ctx := context.Background()
	if *timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, *timeout)
		defer cancel()
	}

	// Everything that can fail must fail before the first write to stdout below:
	// client-go decodes stdout as an ExecCredential, so a half-written document
	// would surface as a confusing decode error instead of the real problem.
	var (
		token  string
		expiry time.Time
		err    error
	)
	switch source {
	case sourceWorkloadAPI:
		socketPath, ok := os.LookupEnv("SPIFFE_ENDPOINT_SOCKET")
		if !ok {
			socketPath = "unix:///tmp/spire-agent/public/api.sock"
		}
		// Unset or empty means "no preference": use the first JWT-SVID the Workload
		// API returns, which is what this plugin has always done. Set this when the
		// Workload API returns several JWT-SVIDs (for example a SPIRE HA broker
		// fronting multiple entry-scoped SVIDs) to pin a specific one.
		hint := os.Getenv("SPIFFE_JWT_HINT")
		token, expiry, err = fetchFromWorkloadAPI(ctx, socketPath, audience, hint)
	case sourceServerAdminAPI:
		serverSocket, ok := os.LookupEnv("SPIRE_SERVER_SOCKET")
		if !ok {
			serverSocket = "unix:///tmp/spire-server/private/api.sock"
		}
		spiffeID, ok := os.LookupEnv("SPIFFE_ID")
		if !ok {
			log.Fatalf("SPIFFE_ID is required when SPIFFE_JWT_SOURCE=%s", sourceServerAdminAPI)
		}
		token, expiry, err = mintFromServerAdminAPI(ctx, serverSocket, spiffeID, audience)
	default:
		log.Fatalf("unknown SPIFFE_JWT_SOURCE %q: must be %q or %q",
			source, sourceWorkloadAPI, sourceServerAdminAPI)
	}
	if err != nil {
		log.Fatal(err)
	}

	now := time.Now()
	expiration, err := metav1.NewTime(credentialExpiration(now, expiry)).MarshalJSON()
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("apiVersion: client.authentication.k8s.io/%s\n", execCredentialVersion)
	fmt.Print("kind: ExecCredential\n")
	fmt.Print("spec:\n")
	fmt.Print(" interactive: false\n")
	fmt.Print("status:\n")
	fmt.Printf("  expirationTimestamp: %s\n", string(expiration))
	fmt.Printf("  token: %s\n", token)
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

// fetchFromWorkloadAPI returns a JWT-SVID of the calling workload from the SPIFFE
// Workload API socket at socketPath, selecting one by hint when several are returned.
func fetchFromWorkloadAPI(ctx context.Context, socketPath, audience, hint string) (string, time.Time, error) {
	jwtSource, err := workloadapi.NewJWTSource(
		ctx,
		workloadapi.WithClientOptions(workloadapi.WithAddr(socketPath)),
	)
	if err != nil {
		return "", time.Time{}, err
	}

	svids, err := jwtSource.FetchJWTSVIDs(ctx, jwtsvid.Params{
		Audience: audience,
	})
	if err != nil {
		return "", time.Time{}, err
	}
	svid, err := selectSVIDByHint(svids, hint)
	if err != nil {
		return "", time.Time{}, err
	}
	return svid.Marshal(), svid.Expiry, nil
}

// mintFromServerAdminAPI mints a JWT-SVID for spiffeID directly from the SPIRE Server
// admin API socket at target.
func mintFromServerAdminAPI(ctx context.Context, target, spiffeID, audience string) (string, time.Time, error) {
	id, err := spiffeid.FromString(spiffeID)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("invalid SPIFFE_ID %q: %w", spiffeID, err)
	}

	conn, err := grpc.NewClient(
		target,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(grpc.WaitForReady(true)),
	)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("failed to dial SPIRE server socket: %w", err)
	}

	resp, err := svidv1.NewSVIDClient(conn).MintJWTSVID(ctx, &svidv1.MintJWTSVIDRequest{
		Id: &apitypes.SPIFFEID{
			TrustDomain: id.TrustDomain().Name(),
			Path:        id.Path(),
		},
		Audience: []string{audience},
	})
	if err != nil {
		return "", time.Time{}, fmt.Errorf("failed to mint JWT-SVID: %w", err)
	}
	if resp.Svid == nil {
		return "", time.Time{}, errors.New("no JWT-SVID in mint response")
	}
	return resp.Svid.Token, time.Unix(resp.Svid.ExpiresAt, 0), nil
}
