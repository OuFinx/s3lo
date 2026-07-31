# sign

Sign an image manifest with a cryptographic key. The signature is stored in S3 alongside the manifest and can be verified with [`s3lo verify`](verify.md).

```
s3lo sign <s3-ref> --key <key-ref>
```

The reference must include an explicit tag. Both `s3://` and `local://` references are supported.

## Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--key` | — | Signing key reference (required). See [Key formats](#key-formats) |
| `--output` | `text` | Output format: `text` or `json` |

## Key formats

| Format | Example | Use case |
|--------|---------|----------|
| AWS KMS alias | `awskms:///alias/release-signer` | Production / FedRAMP |
| AWS KMS ARN | `awskms:///arn:aws:kms:us-east-1:123456789012:key/mrk-abc` | Production / FedRAMP |
| Local key file | `./cosign.key` | Dev, CI with secrets manager |
| HashiCorp Vault | `hashivault://vault.example/v1/transit/keys/ci-signer` | On-prem / multi-cloud |

Note the triple slash (`awskms:///`) — the empty segment between `//` and the path is required by the cosign KMS reference format.

## Examples

```bash
# Sign with AWS KMS (recommended for production / FedRAMP)
s3lo sign s3://my-bucket/myapp:v1.0 --key awskms:///alias/release-signer

# Sign with a KMS key ARN
s3lo sign s3://my-bucket/myapp:v1.0 \
  --key "awskms:///arn:aws:kms:us-east-1:123456789012:key/mrk-abc123"

# Sign with a local key file (set COSIGN_PASSWORD if key is encrypted)
COSIGN_PASSWORD=secret s3lo sign s3://my-bucket/myapp:v1.0 --key cosign.key

# Sign a local:// image
COSIGN_PASSWORD=secret s3lo sign local://./local-s3/alpine:latest --key cosign.key

# JSON output for CI
s3lo sign s3://my-bucket/myapp:v1.0 --key awskms:///alias/release-signer --output json
```

## Output

```
Signed myapp:v1.0
  Digest:  sha256:25109184c71b...
  Key:     awskms:///alias/release-signer
  Key ID:  3b780f64bd6940e1
  Signed:  2026-04-12T19:21:10Z
  Stored:  my-bucket/manifests/myapp/v1.0/signatures/3b780f64bd6940e1.json
```

JSON output (`--output json`):

```json
{
  "digest": "sha256:25109184c71b...",
  "keyID": "3b780f64bd6940e1",
  "keyRef": "awskms:///alias/release-signer",
  "signedAt": "2026-04-12T19:21:10Z",
  "storedPath": "my-bucket/manifests/myapp/v1.0/signatures/3b780f64bd6940e1.json"
}
```

## How signatures are stored

Signatures are stored at:
```
manifests/<image>/<tag>/signatures/<keyid>.json
```

The `keyid` is a 16-character hex fingerprint derived from the SHA-256 of the public key's DER encoding. This means the same physical key always produces the same filename, regardless of how it is referenced (alias vs. ARN vs. file path).

Multiple signatures are supported — each signing key gets its own file. This allows re-signing after key rotation while keeping the old signature valid during the transition window.

## Signature schema

Each `.json` file contains:

```json
{
  "schemaVersion": 1,
  "digest":    "sha256:...",
  "keyRef":    "awskms:///alias/release-signer",
  "keyID":     "3b780f64bd6940e1",
  "algorithm": "ECDSA_SHA_256",
  "signature": "<base64 DER signature>",
  "payload":   "<base64 of signed bytes>",
  "signedAt":  "2026-04-12T19:21:10Z"
}
```

The signed payload is `sha256:<digest>\n` — the manifest digest followed by a newline.

## Local key generation

s3lo uses the [cosign](https://github.com/sigstore/cosign) key format. Generate a key pair with:

```bash
cosign generate-key-pair
```

This creates `cosign.key` (encrypted private key) and `cosign.pub`. Set `COSIGN_PASSWORD` to the key password when signing.

## SOC2 / FedRAMP compliance

Using AWS KMS as the signing key:

- The private key **never leaves the HSM** — signing happens server-side inside KMS.
- Every `kms:Sign` call is recorded in **CloudTrail** → automatic, tamper-evident audit log.
- Access controlled by IAM — create a separate signing role distinct from push access (separation of duties).
- AWS KMS uses **FIPS 140-2 Level 2** validated hardware (Level 3 in GovCloud regions).
- Key rotation is built in (`EnableKeyRotation`).

See also: [`s3lo verify`](verify.md)
