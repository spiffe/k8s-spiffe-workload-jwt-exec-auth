package main

import (
	"context"
	"os"
	"fmt"
	"log"
	"time"

	"github.com/spiffe/go-spiffe/v2/svid/jwtsvid"
	"github.com/spiffe/go-spiffe/v2/workloadapi"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func main() {
	socketPath, ok := os.LookupEnv("SPIFFE_ENDPOINT_SOCKET")
	if !ok {
		socketPath = "unix:///tmp/spire-agent/public/api.sock"
	}

	audience, ok := os.LookupEnv("SPIFFE_JWT_AUDIENCE")
	if !ok {
		audience = "k8s"
	}

	ctx := context.Background()

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

	d := svid.Expiry.Sub(time.Now()) / 2
	expiry, err := metav1.NewTime(svid.Expiry.Add(d)).MarshalJSON()
	if err != nil {
		log.Fatal(err)
	}
	fmt.Print("apiVersion: client.authentication.k8s.io/v1\n")
	fmt.Print("kind: ExecCredential\n")
	fmt.Print("spec:\n")
	fmt.Print(" interactive: false\n")
	fmt.Print("status:\n")
	fmt.Printf("  expirationTimestamp: %s\n", string(expiry))
	fmt.Printf("  token: %s\n", svid.Marshal())
}
