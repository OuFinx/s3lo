# Contributing to s3lo

Thanks for your interest in contributing to s3lo!

## Getting Started

1. Fork the repository
2. Clone your fork: `git clone https://github.com/YOUR-USERNAME/s3lo.git`
3. Create a branch: `git checkout -b my-feature`
4. Make your changes
5. Run tests: `go test ./...`
6. Commit and push
7. Open a Pull Request

## Development

### Prerequisites

- Go 1.22+
- Docker (for integration tests)
- AWS credentials (for S3 integration tests)

### Build

```bash
make build
```

### Test

```bash
# Unit tests only
make test

# With Docker integration tests
S3LO_TEST_DOCKER=1 make test

# With S3 integration tests
S3LO_TEST_BUCKET=your-bucket S3LO_TEST_DOCKER=1 make test
```

### Project Structure

```
s3lo/
├── cmd/s3lo/       # CLI commands (cobra)
├── pkg/
│   ├── ref/        # S3 reference parser (s3://bucket/image:tag)
│   ├── oci/        # OCI image layout operations
│   ├── s3/         # S3 client, upload, download
│   └── image/      # High-level push/pull/list/inspect
├── e2e_test.go     # End-to-end tests
└── .goreleaser.yml # Release configuration
```

### Code Style

- Follow standard Go conventions
- Run `go vet ./...` before submitting
- Keep functions focused and small
- Write tests for new functionality

## Reporting Issues

- Use GitHub Issues
- Include Go version, OS, and s3lo version (`s3lo version`)
- Include the full error message
- Include steps to reproduce

## Pull Requests

- Keep PRs focused on a single change
- Update tests for new functionality
- Update README if adding user-facing features
- Reference related issues in the PR description
