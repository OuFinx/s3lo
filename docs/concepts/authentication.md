# Authentication

s3lo uses the standard AWS SDK credential chain. It does not have its own authentication mechanism — any valid AWS credentials work.

## Credential resolution order

Credentials are resolved in this order:

1. `AWS_ACCESS_KEY_ID` + `AWS_SECRET_ACCESS_KEY` environment variables
2. `AWS_PROFILE` environment variable (selects a named profile)
3. `~/.aws/credentials` and `~/.aws/config` (default profile)
4. IAM instance profile (EC2, ECS task role, Lambda)
5. EKS Pod Identity / IRSA (IAM Roles for Service Accounts)
6. OIDC web identity token (GitHub Actions, GitLab CI)

## Region auto-detection

s3lo auto-detects the bucket region using `s3:GetBucketLocation`. You do not need to set `AWS_REGION` or pass a region flag.

## Common setups

=== "Local development"

    If you have AWS CLI configured, s3lo picks up the same credentials:

    ```bash
    # Uses default profile
    s3lo push myapp:v1.0 s3://my-bucket/myapp:v1.0

    # Use a named profile
    AWS_PROFILE=prod s3lo push myapp:v1.0 s3://my-bucket/myapp:v1.0
    ```

=== "AWS SSO"

    ```bash
    aws sso login --profile prod
    AWS_PROFILE=prod s3lo push myapp:v1.0 s3://my-bucket/myapp:v1.0
    ```

=== "EC2 / ECS"

    No configuration needed — s3lo picks up the instance/task IAM role automatically.

=== "GitHub Actions (OIDC)"

    The recommended approach — no long-lived credentials needed:

    ```yaml
    permissions:
      id-token: write
      contents: read

    steps:
      - uses: OuFinx/s3lo-action@v1
        with:
          role-to-assume: arn:aws:iam::123456789012:role/my-role
          aws-region: us-east-1

      - run: s3lo push myapp:${{ github.sha }} s3://my-bucket/myapp:${{ github.sha }}
    ```

    See the [GitHub Actions guide](../guides/github-actions.md) for full examples.

=== "Environment variables"

    ```bash
    AWS_ACCESS_KEY_ID=AKIA... \
    AWS_SECRET_ACCESS_KEY=... \
    s3lo push myapp:v1.0 s3://my-bucket/myapp:v1.0
    ```

## ECR sources

When using `s3lo copy` with an ECR source, s3lo calls `ecr:GetAuthorizationToken` automatically using the same credential chain. No separate ECR login step is required.

```bash
# Auto-authenticates to ECR using your AWS credentials
s3lo copy 123456789.dkr.ecr.us-east-1.amazonaws.com/myapp:v1.0 s3://my-bucket/myapp:v1.0
```

## No AWS CLI required

s3lo uses the AWS SDK directly. You don't need the AWS CLI installed — only valid credentials.
