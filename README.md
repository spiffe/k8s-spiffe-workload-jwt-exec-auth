# k8s-spiffe-workload-jwt-exec-auth

[![Apache 2.0 License](https://img.shields.io/github/license/spiffe/helm-charts)](https://opensource.org/licenses/Apache-2.0)
[![Development Phase](https://github.com/spiffe/spiffe/blob/main/.img/maturity/dev.svg)](https://github.com/spiffe/spiffe/blob/main/MATURITY.md#development)

A Kubernetes exec auth plugin using the SPIFFE Workload API to get JWTs for auth.

## Building

```
go build -o k8s-spiffe-workload-jwt-exec-auth ./cmd
```

## Configuration

The plugin is configured entirely through environment variables, set via the `env:` list of the
kubeconfig `exec` block:

| Variable | Default | Description |
| --- | --- | --- |
| `SPIFFE_ENDPOINT_SOCKET` | `unix:///tmp/spire-agent/public/api.sock` | Address of the SPIFFE Workload API socket. |
| `SPIFFE_JWT_AUDIENCE` | `k8s` | Audience requested for the JWT-SVID. Must match an entry in the API server's `AuthenticationConfiguration`. |
| `SPIFFE_JWT_HINT` | *(unset)* | Selects which JWT-SVID to use by hint, when the Workload API returns more than one. See below. |
| `EXEC_CREDENTIAL_VERSION` | `v1` | The `client.authentication.k8s.io` version emitted. Use `v1beta1` for older clients. Must match the `apiVersion` in the `exec` block. |

There is also one flag, passed via `args:` rather than `env:`:

| Flag | Default | Description |
| --- | --- | --- |
| `-timeout` | `0` | Max time to wait for the JWT-SVID from the Workload API socket, e.g. `-timeout=5s`. `0` waits forever. |

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
