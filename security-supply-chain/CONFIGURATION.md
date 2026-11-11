# Configuration

| Value | Default | Description |
|---|---|---|
| `scan.image.repository` | `docker.io/aquasec/trivy` | Trivy image for the scan step and the server |
| `scan.image.tag` | `0.69.3` | |
| `scan.mode` | `audit` | `audit` reports findings; `enforce` fails the build on fixable findings at `scan.severity` |
| `scan.severity` | `CRITICAL,HIGH` | Severities that count as findings |
| `scan.ignoreFile` | `.trivyignore` | Ignore file in the repository root; `<name>.yaml` is picked up as well |
| `scan.scanners` | `vuln` | Trivy scanners |
| `scan.timeout` | `10m` | |
| `scan.dbRepository` | `""` | Vulnerability DB mirror (ignored with `server.enabled`) |
| `scan.javaDbRepository` | `""` | Java DB mirror (ignored with `server.enabled`) |
| `scan.extraEnv` | `[]` | Extra environment, e.g. `HTTPS_PROXY` |
| `scan.resources` | see values | |
| `signing.enabled` | `true` | Render the `sign-image`, `attest` and `sign-and-attest` templates |
| `signing.image.repository` | `ghcr.io/sigstore/cosign/cosign` | |
| `signing.image.tag` | `v2.6.5` | |
| `signing.key` | `""` | cosign key reference; required when signing is enabled |
| `signing.tlogUpload` | `false` | Upload to a Rekor transparency log |
| `signing.extraArgs` | `[]` | Extra cosign arguments |
| `signing.extraEnv` | `[]` | Environment for cosign, e.g. `VAULT_ADDR` |
| `signing.secretEnv` | `[]` | Environment from Secrets: `{name, secretName, key}` |
| `signing.attestations.sbom` | `true` | Attach the CycloneDX SBOM |
| `signing.attestations.vulnerabilities` | `true` | Attach the fixable findings (`cosign-vuln`) |
| `signing.attestations.provenance` | `true` | Attach SLSA v1 provenance |
| `workflow.templateName` | `image-security` | Name of the `ClusterWorkflowTemplate` |
| `workflow.workspaceVolume` | `workspace` | Workspace volume claim of the calling workflow |
| `workflow.imageTarPath` | `/mnt/vol/app-image.tar` | Image tarball written by the build step |
| `workflow.sourcePath` | `/mnt/vol/source` | Checked-out repository |
| `workflow.reportDir` | `/mnt/vol/security` | Where reports and predicates are written |
| `workflow.registryPushSecretParameter` | `registry-push-secret` | Workflow parameter naming the registry push Secret |
| `workflow.builderId` | `https://github.com/openchoreo/community-modules/tree/main/security-supply-chain` | Builder ID in the provenance |
| `server.enabled` | `false` | Deploy a Trivy server; builds then run in client mode |
| `server.port` | `4954` | |
| `server.replicas` | `1` | |
| `server.extraEnv` | `[]` | Extra environment, e.g. `HTTPS_PROXY` |
| `server.dbRepository` | `""` | Vulnerability DB mirror |
| `server.javaDbRepository` | `""` | Java DB mirror |
| `server.persistence.enabled` | `true` | Keep the databases on a PVC |
| `server.persistence.size` | `5Gi` | |
| `server.persistence.storageClassName` | `""` | |
| `server.resources` | see values | |

## Outputs

`scan-image` writes to `workflow.reportDir`:

| File | Content |
|---|---|
| `report.json` | Full Trivy report, including findings without a fix |
| `sbom.cdx.json` | CycloneDX SBOM |
| `vuln.cosign.json` | Fixable findings, cosign vulnerability predicate |
| `provenance.json` | SLSA v1 provenance predicate |

Its output parameter `result` is `passed` or `findings`.
