package main

import (
	"context"
	"os"
	"log"
	"time"

	"github.com/spiffe/go-spiffe/v2/svid/jwtsvid"
	"github.com/spiffe/go-spiffe/v2/workloadapi"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"k8s.io/client-go/pkg/apis/clientauthentication"
	"k8s.io/cli-runtime/pkg/printers"
)

func main() {
	cred := &clientauthentication.ExecCredential{
		Status: &clientauthentication.ExecCredentialStatus {},
	}

	socketPath, ok := os.LookupEnv("SPIFFE_ENDPOINT_SOCKET")
	if !ok {
		socketPath = "unix:///tmp/spire-agent/public/api.sock"
	}

	audience, ok := os.LookupEnv("SPIFFE_JWT_AUDIENCE")
	if !ok {
		audience = "k8s"
	}

	cred.APIVersion = "client.authentication.k8s.io/v1"
	cred.Kind = "ExecCredentials"
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
	expiry := metav1.NewTime(svid.Expiry.Add(d))
	cred.Status.ExpirationTimestamp = &expiry
	cred.Status.Token = svid.Marshal()

	y := printers.YAMLPrinter{}
	y.PrintObj(cred, os.Stdout)
}
