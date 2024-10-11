# k8s-spiffe-workload-jwt-exec-auth
A Kubernetes exec auth plugin using the spiffe workload api to get jwts for auth

## Building
go build .

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

### Kubeconfig file

Start with a copy of your kubernetes clusters /etc/kubernetes/admin.conf file.

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
```
