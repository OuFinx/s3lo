# verify

Verify that a stored image signature matches the current manifest digest. Use this as a gate in CI/CD pipelines to ensure only signed images are deployed.

```
s3lo verify <s3-ref> --key <key-ref>
```

The reference must include an explicit tag. Both `s3://` and `local://` references are supported.

## Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--key` | — | Verification key reference (required). See [Key formats](#key-formats) |
| `--output` | `text` | Output format: `text` or `json` |

## Exit codes

| Code | Meaning |
|------|---------|
| `0` | Signature is valid |
| `1` | Signature is missing or cryptographically invalid |
| `2` | Infrastructure error (cannot reach S3, KMS, etc.) |

Separating `1` (supply-chain failure) from `2` (infrastructure failure) lets CI pipelines distinguish between a broken build gate and a broken environment.

## Key formats

| Format | Example |
|--------|---------|
| AWS KMS alias | `awskms:///alias/release-signer` |
| AWS KMS ARN | `awskms:///arn:aws:kms:us-east-1:123456789012:key/mrk-abc` |
| Local public key file | `./cosign.pub` |
| Local private key file | `./cosign.key` (public key is derived from it) |
| HashiCorp Vault | `hashivault://vault.example/v1/transit/keys/ci-signer` |

Note the triple slash (`awskms:///`) — required by the cosign KMS reference format.

## Examples

```bash
# Verify with AWS KMS
s3lo verify s3://my-bucket/myapp:v1.0 --key awskms:///alias/release-signer

# Verify with a local public key file
s3lo verify s3://my-bucket/myapp:v1.0 --key cosign.pub

# Verify a local:// image
s3lo verify local://./local-s3/alpine:latest --key cosign.pub

# CI gate — exits 1 on failure, 2 on infrastructure error
s3lo verify s3://my-bucket/myapp:v1.0 --key awskms:///alias/release-signer \
  && echo "Deploy approved" || echo "Deploy blocked"

# Machine-readable output
s3lo verify s3://my-bucket/myapp:v1.0 --key cosign.pub --output json
```

## Output

Success:
```
✓ Verified myapp:v1.0
  Digest:  sha256:25109184c71b...
  Signed:  2026-04-12T19:21:10Z
  Key:     awskms:///alias/release-signer
```

Failure (missing signature):
```
✗ Verification FAILED for myapp:v1.0
  no signature found for key cosign.pub
```

Failure (tampered image):
```
✗ Verification FAILED for myapp:v1.0
  manifest changed: signed sha256:25109184..., current sha256:deadbeef...
```

JSON output (`--output json`):

```json
{
  "verified": true,
  "digest": "sha256:25109184c71b...",
  "keyRef": "awskms:///alias/release-signer",
  "keyID": "3b780f64bd6940e1",
  "signedAt": "2026-04-12T19:21:10Z"
}
```

On failure, `verified` is `false` and `reason` is set:

```json
{
  "verified": false,
  "reason": "no signature found for key cosign.pub",
  "digest": "sha256:25109184c71b...",
  "keyRef": "cosign.pub",
  "keyID": "21b44047667b31d3"
}
```

## CI integration

### GitHub Actions

```yaml
- name: Verify image signature
  run: |
    s3lo verify s3://my-bucket/myapp:${{ github.sha }} \
      --key awskms:///alias/release-signer \
      --output json | tee verify-result.json
  env:
    AWS_REGION: us-east-1
```

The `--output json` result can be saved as a build artifact for audit evidence.

### Deployment gate

```bash
#!/bin/bash
s3lo verify s3://my-bucket/myapp:${VERSION} --key awskms:///alias/release-signer
if [ $? -ne 0 ]; then
  echo "Image verification failed. Refusing deployment."
  exit 1
fi
kubectl set image deployment/myapp myapp=...
```

## What is verified

`verify` checks three things:

1. **Signature exists** — a `.json` file for this key's fingerprint is present at `manifests/<image>/<tag>/signatures/<keyid>.json`
2. **Digest matches** — the digest stored in the signature matches the current manifest's SHA-256
3. **Signature is valid** — the cryptographic signature bytes verify against the stored digest and the provided key

If any check fails, the command exits `1` with a descriptive message.

## Multiple signers

If an image has been signed by multiple keys, each key produces its own signature file. Run `verify` once per key you want to check:

```bash
s3lo verify s3://my-bucket/myapp:v1.0 --key awskms:///alias/ci-signer
s3lo verify s3://my-bucket/myapp:v1.0 --key awskms:///alias/release-signer
```

Use [`s3lo inspect`](inspect.md) to see which keys have signed an image.

See also: [`s3lo sign`](sign.md), [`s3lo inspect`](inspect.md)
