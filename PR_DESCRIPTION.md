## Add inbound fallback TLS Secret for non-meshed clients

Fixes #2227

### Problem

A service that already serves its own certificate cannot onboard to Linkerd transparently. Once injected, the proxy only presents the Linkerd-issued identity on inbound connections, so every legacy client that pins the application's certificate must either switch to Linkerd's trust roots at the same time or be routed out and back in through an ingress that knows the old certificate. Neither option is transparent, and the second loses mesh observability for that hop.

### Solution

Let a workload hand its existing certificate to the proxy. When a `kubernetes.io/tls` Secret is named, the injector mounts it into the proxy container and tells the proxy where the certificate and key live. The proxy keeps using its Linkerd identity for meshed (proxy-to-proxy) connections and serves the provided certificate on inbound TLS connections that do not originate from another Linkerd proxy.

Three equivalent ways to enable it, matching how other proxy config is exposed:

| Surface | Name |
| --- | --- |
| Workload or namespace annotation | `config.linkerd.io/inbound-fallback-tls-secret: <secret-name>` |
| `linkerd inject` flag | `--inbound-fallback-tls-secret <secret-name>` |
| Helm value | `proxy.inboundFallbackTLSSecret` |

The Secret must live in the workload's namespace and contain the standard `tls.crt` and `tls.key` keys.

### What the injected pod looks like

For `--inbound-fallback-tls-secret emoji-legacy-tls`, the patch adds:

```yaml
# proxy container
env:
- name: LINKERD2_PROXY_INBOUND_FALLBACK_TLS_CERT_PATH
  value: /var/run/linkerd/tls/inbound-fallback/tls.crt
- name: LINKERD2_PROXY_INBOUND_FALLBACK_TLS_KEY_PATH
  value: /var/run/linkerd/tls/inbound-fallback/tls.key
volumeMounts:
- mountPath: /var/run/linkerd/tls/inbound-fallback
  name: linkerd-inbound-fallback-tls
  readOnly: true

# pod
volumes:
- name: linkerd-inbound-fallback-tls
  secret:
    secretName: emoji-legacy-tls
    defaultMode: 0400
    items:
    - key: tls.crt
      path: tls.crt
    - key: tls.key
      path: tls.key
```

The full rendered output is in `cli/cmd/testdata/inject_emojivoto_deployment_inbound_fallback_tls.golden.yml`.

### Changes

**Config surface**
- `pkg/k8s/labels.go`: new `ProxyInboundFallbackTLSSecretAnnotation` and `InboundFallbackTLSVolumeName` constants.
- `pkg/charts/linkerd2/values.go`: new `Proxy.InboundFallbackTLSSecret` field.
- `charts/linkerd-control-plane/values.yaml`: new `proxy.inboundFallbackTLSSecret` value (default empty) with helm-docs comment.

**Injection**
- `pkg/inject/inject.go`: annotation added to `ProxyAnnotations` so it is inherited from the namespace like every other proxy override. The value is validated as a DNS-1123 subdomain via a new `ValidateInboundFallbackTLSSecret` helper. Invalid values are logged and ignored, consistent with how other malformed overrides are handled by the webhook.
- `charts/partials/templates/_proxy.tpl`: emits the two env vars and the read-only volume mount. Both are gated on `proxy.component` being unset, so control-plane proxies (identity, destination, proxy-injector) never receive the mount even when the Helm value is set globally. Without that gate a global value would give control-plane pods a volume mount with no backing volume.
- `charts/partials/templates/_volumes.tpl`: new `partials.proxy.volumes.inbound-fallback-tls` partial projecting only `tls.crt` and `tls.key` with mode `0400`.
- `charts/patch/templates/patch.json`: appends the Secret volume to the JSON patch.
- `pkg/inject/uninject.go`: `linkerd uninject` removes the projected volume along with the other proxy volumes.

**CLI**
- `cli/cmd/options.go`: `--inbound-fallback-tls-secret` flag on `linkerd inject`, validated up front so a bad name fails fast instead of silently producing an un-schedulable pod.
- `cli/cmd/inject.go`: the flag is recorded as the annotation on the injected workload so re-injection and the webhook agree.
- `cli/cmd/doc.go`: annotation documented in the generated reference.

### Behavior notes

- **Fail closed on a missing Secret.** The volume is not marked `optional`, so if the Secret does not exist the pod stays in `ContainerCreating` with a clear `FailedMount` event rather than starting a proxy that cannot serve the promised certificate.
- **Rotation.** Kubernetes refreshes projected Secret volumes in place, so a rotated certificate lands on disk without a restart. Whether the proxy re-reads it is up to the proxy implementation (see below).
- **No new RBAC.** The kubelet mounts the Secret, so neither the CLI nor the injector needs read access to Secrets.
- **Control plane unaffected.** The Helm value is stored in `linkerd-config` for the injector to pick up, but control-plane deployments render identically with or without it (verified with `linkerd install --set proxy.inboundFallbackTLSSecret=...`).

### Proxy-side contract

This PR delivers the control-plane, CLI, and chart half of the feature. The proxy (linkerd2-proxy) needs a companion change to honor the two environment variables:

| Variable | Meaning |
| --- | --- |
| `LINKERD2_PROXY_INBOUND_FALLBACK_TLS_CERT_PATH` | PEM certificate chain to present when the inbound peer is not a Linkerd proxy |
| `LINKERD2_PROXY_INBOUND_FALLBACK_TLS_KEY_PATH` | Matching PEM private key |

Expected proxy semantics: on an inbound TLS handshake, if the ClientHello carries the Linkerd identity SNI (or otherwise identifies a meshed peer) serve the Linkerd identity as today; otherwise serve the fallback certificate. When both variables are unset, behavior is unchanged. Until the proxy change ships, the env vars and mount are inert.

### Tests

- `pkg/inject`: annotation applied from workload and namespace in `TestGetOverriddenValues`; new `TestApplyAnnotationOverridesInboundFallbackTLSSecret` covering override precedence, empty-string disable, and rejection of malformed names; new `TestValidateInboundFallbackTLSSecret`.
- `cli/cmd`: new inject golden `inject_emojivoto_deployment_inbound_fallback_tls.golden.yml` and a matching uninject case proving the volume is stripped while the `config.linkerd.io` annotation is retained.
- Install golden files regenerated for the new `linkerd-config` value (one added line each).

```
go test ./pkg/inject/... ./pkg/k8s/... ./pkg/charts/... ./cli/cmd/... ./controller/proxy-injector/...
ok  github.com/linkerd/linkerd2/pkg/inject
ok  github.com/linkerd/linkerd2/pkg/k8s
ok  github.com/linkerd/linkerd2/pkg/charts
ok  github.com/linkerd/linkerd2/pkg/charts/linkerd2
ok  github.com/linkerd/linkerd2/cli/cmd
ok  github.com/linkerd/linkerd2/controller/proxy-injector
```

### Try it

```bash
kubectl -n emojivoto create secret tls emoji-legacy-tls --cert=legacy.crt --key=legacy.key
kubectl -n emojivoto get deploy web -o yaml \
  | linkerd inject --inbound-fallback-tls-secret emoji-legacy-tls - \
  | kubectl apply -f -
kubectl -n emojivoto get pod -l app=web-svc -o jsonpath='{.items[0].spec.volumes[?(@.name=="linkerd-inbound-fallback-tls")]}'
```

🤖 Generated with [Claude Code](https://claude.com/claude-code)
