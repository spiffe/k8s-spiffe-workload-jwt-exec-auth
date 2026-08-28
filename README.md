# k8s-spiffe-workload-jwt-exec-auth

[![Apache 2.0 License](https://img.shields.io/github/license/spiffe/helm-charts)](https://opensource.org/licenses/Apache-2.0)
[![Development Phase](https://github.com/spiffe/spiffe/blob/main/.img/maturity/dev.svg)](https://github.com/spiffe/spiffe/blob/main/MATURITY.md#development)

A Kubernetes exec auth plugin that gets a SPIFFE JWT-SVID for authentication,
either from the SPIFFE Workload API (default) or by minting one from the SPIRE
Server admin API. It can optionally exchange that JWT-SVID for a token issued by
a token exchange before presenting it.

## Server config

If you want to use this plugin along with the discovery provider secured by SPIFFE rather then webPKI, you can configure the apiserver using https://github.com/spiffe/k8s-spiffe-workload-auth-config.

## Building

```
go build -o k8s-spiffe-workload-jwt-exec-auth ./cmd
```

## Configuration

The plugin is configured entirely through environment variables, set via the `env:` list of the
kubeconfig `exec` block:

| Variable | Default | Description |
| --- | --- | --- |
| `SPIFFE_JWT_SOURCE` | `workload-api` | Where the JWT-SVID comes from: `workload-api` or `server-admin-api`. See below. |
| `SPIFFE_ENDPOINT_SOCKET` | `unix:///tmp/spire-agent/public/api.sock` | Address of the SPIFFE Workload API socket. `workload-api` source only. |
| `SPIFFE_JWT_AUDIENCE` | `k8s` | Audience requested for the JWT-SVID. Must match an entry in the API server's `AuthenticationConfiguration`, unless an exchange requires otherwise. |
| `SPIFFE_JWT_HINT` | *(unset)* | Selects which JWT-SVID to use by hint, when the Workload API returns more than one. `workload-api` source only. See below. |
| `EXEC_CREDENTIAL_VERSION` | `v1` | The `client.authentication.k8s.io` version emitted. Use `v1beta1` for older clients. Must match the `apiVersion` in the `exec` block. |
| `SPIRE_SERVER_SOCKET` | `unix:///tmp/spire-server/private/api.sock` | Address of the SPIRE Server admin API socket. `server-admin-api` source only. |
| `SPIFFE_ID` | *(unset)* | The SPIFFE ID to mint a JWT-SVID for. Required for the `server-admin-api` source. |
| `SPIFFE_JWT_EXCHANGE_ENDPOINT` | *(unset)* | RFC 8693 token exchange endpoint, `https://` only. See below. |
| `SPIFFE_JWT_EXCHANGE_AUDIENCE` | *(unset)* | RFC 8693 `audience` for the issued token, sent only when set. See below. |

There is also one flag, passed via `args:` rather than `env:`:

| Flag | Default | Description |
| --- | --- | --- |
| `-timeout` | `0` | Max time to wait for the credential, including any token exchange, e.g. `-timeout=5s`. `0` waits forever. |

### Choosing a JWT source with `SPIFFE_JWT_SOURCE`

By default the plugin fetches the JWT-SVID of the calling workload from the SPIFFE Workload API. Set
`SPIFFE_JWT_SOURCE` to `server-admin-api` to instead mint a JWT-SVID for `SPIFFE_ID` directly from the
SPIRE Server admin API socket (`SPIRE_SERVER_SOCKET`).

The `server-admin-api` source is useful where the Workload API is not reachable but the server admin
socket is — for example an exec consumer co-located with spire-server — and where the credential
should not depend on agent attestation. That consumer must have access to the server admin socket.

### Selecting an identity with `SPIFFE_JWT_HINT`

Hints are operator-set strings on SPIRE registration entries, used "to provide guidance on how this
identity should be used by a workload when more than one SVID is returned". If the Workload API
returns several JWT-SVIDs — for example a SPIRE HA broker fronting multiple entry-scoped SVIDs — then
which one comes first is arbitrary, and the plugin may authenticate to the cluster as an identity you
did not intend.

Set `SPIFFE_JWT_HINT` to pin a specific one:

- **Unset or empty**: use the first JWT-SVID returned. This is the original behavior.
- **Set and matched**: use the JWT-SVID with that hint.
- **Set and unmatched**: exit non-zero with an error on stderr listing the hints that *were*
  available, rather than silently authenticating as a different identity.

Matching is exact — no case folding or whitespace trimming. SPIRE keeps only the first SVID for each
non-empty hint, so hints are effectively unique.

### Exchanging the JWT-SVID with `SPIFFE_JWT_EXCHANGE_ENDPOINT`

Some clusters trust a token exchange as their issuer instead of trusting SPIRE directly, and a
JWT-SVID is not a credential those clusters accept. Set `SPIFFE_JWT_EXCHANGE_ENDPOINT` and the
plugin trades the JWT-SVID for a token from that exchange, using the
[RFC 8693](https://www.rfc-editor.org/rfc/rfc8693) token-exchange grant, then presents that
instead:

```
grant_type=urn:ietf:params:oauth:grant-type:token-exchange
subject_token=<the JWT-SVID>
subject_token_type=urn:ietf:params:oauth:token-type:jwt
```

This works with either `SPIFFE_JWT_SOURCE`.

There are two audiences, and which one you need depends on the exchange. `SPIFFE_JWT_AUDIENCE` is
the audience SPIRE puts in the JWT-SVID. `SPIFFE_JWT_EXCHANGE_AUDIENCE` is the `audience` parameter
sent to the exchange, and is only sent if you set it. Google Cloud's STS requires that parameter.
Other exchanges take the audience from the subject token and reject a request that sends one.
Either way, the token you end up with has to carry the audience in the cluster's
`AuthenticationConfiguration`.

If the exchange fails, the plugin exits non-zero and prints the exchange's own `error` and
`error_description`. It does not fall back to the unexchanged JWT-SVID.

- **The endpoint sees a live JWT-SVID** on every refresh, and it comes from a kubeconfig, which
  people copy and share. Treat it as trusted. It has to be `https://`, and redirects are refused —
  following one would send the JWT-SVID to a host that was never checked. For a private CA, add
  that CA to the system trust store.
- **`expires_in` is required**, even though RFC 8693 only recommends it. Without it there is no
  expiry to report, and client-go caches the credential for the life of the process and only
  refreshes it after a 401.
- **`-timeout` covers the exchange**, not just the fetch. Set it — the default waits forever, and
  this is a call to a remote host.
- **No client authentication is sent.** RFC 8693 leaves that to the exchange, and the JWT-SVID is
  itself the credential. If an exchange wants a client secret or client certificate on top of the
  subject token, this plugin cannot talk to it.

## Usage

### Setup the Kubernetes cluster auth

We recommend using the Structured Authentication mechanism, as documented here: https://kubernetes.io/blog/2024/04/25/structured-authentication-moves-to-beta/

As an example:
```yaml
apiVersion: apiserver.config.k8s.io/v1beta1
kind: AuthenticationConfiguration
jwt:
- issuer:
    # Update to point at your spiffe-oidc-discovery-provider
    url: https://oidc-discovery.example.org
    audiences:
    - k8s
  claimMappings:
    username:
      claim: "sub"
      prefix: ""
```

### User kubeconfig file

Start with a copy of your Kubernetes clusters /etc/kubernetes/admin.conf file.

Remove the "user" block from the "users" section and replace it with:
```yaml
  user:
    exec:
      apiVersion: "client.authentication.k8s.io/v1"
      command: "k8s-spiffe-workload-jwt-exec-auth"
      interactiveMode: Never
      # To customize, uncomment and change the settings below
      #env:
      #  - name: SPIFFE_ENDPOINT_SOCKET
      #    value: "unix:///var/run/spire/agent/sockets/main/public/api.sock"
      #  - name: SPIFFE_JWT_AUDIENCE
      #    value: "k8s-one"
      #  - name: SPIFFE_JWT_HINT
      #    value: "k8s-one"
      #args:
      #  - "-timeout=5s"
```

### Kubelet kubeconfig file

Modify `/etc/kubernetes/kubelet.conf`, and remove `client-certificate` and `client-key` settings. Then add the following exec block to user:

```yaml
  user:
    exec:
      apiVersion: "client.authentication.k8s.io/v1"
      command: "k8s-spiffe-workload-jwt-exec-auth"
      interactiveMode: Never
      # To customize, uncomment and change the settings below
      #env:
      #  - name: SPIFFE_ENDPOINT_SOCKET
      #    value: "unix:///var/run/spire/agent/sockets/main/public/api.sock"
      #  - name: SPIFFE_JWT_AUDIENCE
      #    value: "k8s-one"
      #  - name: SPIFFE_JWT_HINT
      #    value: "k8s-one"
      #args:
      #  - "-timeout=5s"
```
