# Supply Chain Security for OpenChoreo

Adds a signed supply chain to the images OpenChoreo builds: every image is
scanned for vulnerabilities before it is pushed, then signed and published
with an SBOM, a vulnerability report and build provenance attached as
in-toto attestations.

The module plugs into the **workflow plane**. It ships Argo
`ClusterWorkflowTemplate` steps that slot into OpenChoreo's build workflows,
and an optional Trivy server that keeps the vulnerability database out of
every build.

## How it works

```
checkout-source → build-image → scan-image → publish-image → sign-and-attest → generate-workload-cr
                                    │                              │
                                    │                              ├─ cosign sign
                                    │                              ├─ attest cyclonedx  (SBOM)
                                    │                              ├─ attest vuln       (fixable findings)
                                    │                              └─ attest slsaprovenance1
                                    └─ trivy on the image tarball
```

- **Only fixable vulnerabilities count.** A finding without a released fix
  gives the component owner nothing to act on, so it is reported but never
  fails a build or a deployment.
- **The scan runs before the push.** `scan-image` reads the tarball the build
  step leaves in the workspace, so nothing unscanned reaches the registry.
- **Audit first.** In `audit` mode the build reports findings and continues;
  `enforce` fails the build on fixable findings at the configured severities.
  Deadlines for existing images are enforced at admission (see Roadmap),
  because only a rescan knows since when a fix has been available.
- **Keys stay in your KMS.** Signing accepts any cosign key reference,
  including `hashivault://` (HashiCorp Vault and OpenBao transit),
  `awskms://`, `gcpkms://`, `azurekms://` and `k8s://`. Uploads to a public
  transparency log are off by default, as they would disclose image and
  repository names.

## Prerequisites

- OpenChoreo with the workflow plane (Argo Workflows) installed.
- A signing key reachable from the workflow namespaces, for example an OpenBao
  transit key:

  ```bash
  bao secrets enable transit
  bao write -f transit/keys/image-signing type=ecdsa-p256
  ```

- Registry credentials that may push signatures and attestations next to the
  image. The default workflows already provide them as the
  `registry-push-secret` workflow parameter.

## Install

```bash
helm upgrade --install security-supply-chain \
  oci://ghcr.io/openchoreo/helm-charts/security-supply-chain \
  --version <VERSION> \
  --namespace openchoreo-workflow-plane \
  --set signing.key=hashivault://image-signing \
  --set 'signing.extraEnv[0].name=VAULT_ADDR' \
  --set 'signing.extraEnv[0].value=https://openbao.example.com' \
  --set 'signing.secretEnv[0].name=VAULT_TOKEN' \
  --set 'signing.secretEnv[0].secretName=image-signing' \
  --set 'signing.secretEnv[0].key=token'
```

The `image-signing` Secret must exist in every namespace where build
workflows run. Prefer a short-lived token, for example from an
external-secrets `VaultDynamicSecret` generator against a token role that may
only sign with the transit key. See [CONFIGURATION.md](CONFIGURATION.md) for
all values.

To keep builds from downloading the vulnerability database on every run, set
`server.enabled=true`; the scan steps then use a Trivy server in the workflow
plane. Behind a proxy, give the server `server.extraEnv` with `HTTPS_PROXY`.

## Wire it into a build workflow

Insert the two steps from
[`samples/dockerfile-builder-steps.yaml`](samples/dockerfile-builder-steps.yaml)
into the `build-workflow` template of each `ClusterWorkflow` that should
produce signed images: `scan-image` between `build-image` and
`publish-image`, `sign-and-attest` between `publish-image` and
`generate-workload-cr`.

## Exceptions

Component owners can accept a finding by adding a `.trivyignore.yaml` to the
root of their repository. Give every entry a reason and an expiry date:

```yaml
vulnerabilities:
  - id: CVE-2026-12345
    statement: Not reachable, the affected function is not used
    expired_at: 2026-12-31
```

## Verify

```bash
cosign public-key --key hashivault://image-signing > image-signing.pub
cosign verify --key image-signing.pub --insecure-ignore-tlog <image>
cosign verify-attestation --key image-signing.pub --insecure-ignore-tlog \
  --type vuln <image>
```

## Developer portal

The OpenChoreo portal can show the result to component owners: the image
security plugin in `openchoreo/backstage-plugins` adds a **Security** tab
listing, per environment, the fixable vulnerabilities of the running image.
It reads the `vuln` attestation this module attaches and verifies its
signature against the public key above.

## Roadmap

1. **Build** — scan, SBOM, signature and attestations *(this release)*.
2. **Admission** — Kyverno `ImageValidatingPolicy` on Pods in OpenChoreo cell
   namespaces: require the signature, provenance and a fresh scan verdict;
   audit first.
3. **Rescans** — a scheduled workflow rescans every running image by digest,
   records when a fix first became available and signs a verdict. Default
   grace periods: **CRITICAL 15 days, HIGH 45 days**.
   - Within the grace period nothing is blocked: restarts, rescheduling and
     node drains go through.
   - What happens once it expires is a setting, `enforcement.mode`:

     | Mode | After the grace period |
     |---|---|
     | `report` *(default)* | reported and alerted only, nothing is blocked |
     | `block` | admission rejects new Pods; running ones keep running |
     | `stop` | admission rejects new Pods, and a Kyverno `DeletingPolicy` removes the running ones; the component stays down until it is rebuilt with the fix |

     The default never takes a workload down just because the module was
     installed; stricter modes are an explicit choice.
   - A namespace label can relax the platform mode for one project
     (e.g. `stop` for the platform, `report` for a project that cannot be
     rebuilt in time); it can never make it stricter.
   - Owners are warned at 50 % and 80 % of the grace period; a
     `PolicyException` with an expiry date covers single emergencies.
4. **Enforce (optional)** — platforms that want it move from `report` to
   `block` or `stop`, after a reporting-only period.
5. **Third-party images** — mirror, scan and countersign images the platform
   does not build itself.
