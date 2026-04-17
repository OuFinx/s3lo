package image

const (
	// blobConcurrency is the parallelism limit for blob upload/download operations.
	blobConcurrency = 10
	// scanConcurrency is the parallelism limit for manifest/metadata scan operations
	// (gc, stats, doctor) where I/O is cheaper and scan breadth benefits from higher concurrency.
	scanConcurrency = 20
)
