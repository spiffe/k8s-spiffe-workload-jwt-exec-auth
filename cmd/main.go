package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/spiffe/go-spiffe/v2/svid/jwtsvid"
	"github.com/spiffe/go-spiffe/v2/workloadapi"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func main() {
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
	svid, err := jwtSource.FetchJWTSVID(ctx, jwtsvid.Params{
		Audience: audience,
	})
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
