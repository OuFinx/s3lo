package storage

import (
	"github.com/aws/aws-sdk-go-v2/feature/s3/manager"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

const (
	// transferPartSize is the part size for both multipart uploads and ranged
	// downloads. S3 caps a multipart upload at 10,000 parts, so 16 MB puts the
	// ceiling at 160 GB per object — far above any realistic image layer.
	// AWS recommends 8-16 MB parts to saturate available bandwidth.
	transferPartSize = 16 << 20 // 16 MB

	// transferConcurrency is how many parts are in flight per object. The OCI
	// Distribution Spec forces registries to move a layer's chunks one at a
	// time; S3 has no such constraint, and this is where that difference is
	// actually spent.
	transferConcurrency = 8
)

// newUploader returns an S3 uploader that sends parts in parallel. It falls back
// to a single PutObject when the body is smaller than one part, retries
// individual parts, and aborts the multipart upload on failure so partial uploads
// do not linger and accrue storage charges.
func newUploader(client *s3.Client) *manager.Uploader {
	return manager.NewUploader(client, func(u *manager.Uploader) {
		u.PartSize = transferPartSize
		u.Concurrency = transferConcurrency
	})
}

// newDownloader returns an S3 downloader that fetches byte ranges in parallel
// into an io.WriterAt. Objects smaller than one part cost a single request.
func newDownloader(client *s3.Client) *manager.Downloader {
	return manager.NewDownloader(client, func(d *manager.Downloader) {
		d.PartSize = transferPartSize
		d.Concurrency = transferConcurrency
	})
}
