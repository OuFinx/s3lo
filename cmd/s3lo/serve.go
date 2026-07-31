package main

import (
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/OuFinx/s3lo/v2/pkg/image"
	"github.com/OuFinx/s3lo/v2/pkg/serve"
	storage "github.com/OuFinx/s3lo/v2/pkg/storage"
	"github.com/spf13/cobra"
)

// Compile-time check: *storage.Client must satisfy serve.Presigner.
var _ serve.Presigner = (*storage.Client)(nil)

var serveCmd = &cobra.Command{
	Use:   "serve <s3-ref>",
	Short: "Serve images via OCI Distribution Spec (docker pull compatible)",
	Long: `Start an HTTP server that speaks the OCI Distribution Spec,
serving images stored in the given bucket.

Enables docker pull, kubectl, and any OCI client to pull images
directly from S3 without running s3lo pull first.

For S3 and S3-compatible backends, blob requests are served via
presigned URL redirects — no data passes through this server.
For GCS, Azure, and local backends, blobs are streamed.`,
	Example: `  Docs: https://oufinx.github.io/s3lo/commands/serve/

  # Serve from S3 on localhost
  s3lo serve s3://my-bucket/ --port 5000

  # Pull from it with Docker
  docker pull localhost:5000/myapp:v1.0

  # Expose on all interfaces with TLS
  s3lo serve s3://my-bucket/ --host 0.0.0.0 --tls-cert cert.pem --tls-key key.pem

  # MinIO / S3-compatible
  s3lo serve s3://my-bucket/ --endpoint http://minio:9000`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		port, _ := cmd.Flags().GetInt("port")
		host, _ := cmd.Flags().GetString("host")
		tlsCert, _ := cmd.Flags().GetString("tls-cert")
		tlsKey, _ := cmd.Flags().GetString("tls-key")
		presignTTL, _ := cmd.Flags().GetDuration("presign-ttl")
		cacheEntries, _ := cmd.Flags().GetInt("cache-entries")
		cacheTTL, _ := cmd.Flags().GetDuration("cache-ttl")

		bucket, scoped, err := image.ParseConfigRef(args[0])
		if err != nil {
			return err
		}
		// serve exposes the whole bucket; it has no notion of a sub-scope. A ref
		// carrying one was accepted and then discarded, so `s3lo serve
		// s3://bucket/team-a` printed "Serving s3://bucket/team-a/" while every
		// other image in the bucket stayed reachable.
		if scoped != "" {
			return fmt.Errorf("serve takes a bucket, not an image or prefix: use %s://%s/ (serving is always bucket-wide)",
				refScheme(args[0]), bucket)
		}

		client, err := storage.NewBackendFromRef(cmd.Context(), args[0])
		if err != nil {
			return err
		}

		srv := serve.NewServer(serve.Server{
			Client:     client,
			Bucket:     bucket,
			PresignTTL: presignTTL,
			// Manifests are re-read on every pull and are kilobytes each, so a small
			// cache removes most of the request traffic at negligible memory cost.
			Cache: serve.NewCache(cacheEntries, cacheTTL),
		})

		addr := fmt.Sprintf("%s:%d", host, port)
		scheme := "http"
		if tlsCert != "" {
			scheme = "https"
		}

		ref := strings.TrimRight(args[0], "/") + "/"

		var blobStrategy string
		if _, ok := client.(serve.Presigner); ok {
			blobStrategy = "presigned URLs (S3)"
		} else if strings.HasPrefix(args[0], "gs://") {
			blobStrategy = "streaming (GCS)"
		} else if strings.HasPrefix(args[0], "az://") {
			blobStrategy = "streaming (Azure)"
		} else {
			blobStrategy = "streaming (local)"
		}

		fmt.Printf("Serving %s at %s://%s\n", ref, scheme, addr)
		fmt.Printf("Blob strategy: %s\n", blobStrategy)

		// serve has no authentication. On a non-loopback bind, every image in the
		// bucket (and presigned S3 blob URLs) is exposed to anyone who can reach the
		// port — warn loudly so it is not left open unintentionally.
		if !isLoopbackHost(host) {
			fmt.Printf("\n⚠️  WARNING: binding to %q with no authentication — every image in the bucket is\n", host)
			fmt.Printf("   readable by anyone who can reach this port. Restrict access at the network layer\n")
			fmt.Printf("   (firewall/security group) or put an authenticating proxy in front.\n")
		}
		fmt.Printf("Press Ctrl+C to stop.\n\n")

		if tlsCert != "" {
			return http.ListenAndServeTLS(addr, tlsCert, tlsKey, srv)
		}
		return http.ListenAndServe(addr, srv)
	},
}

// isLoopbackHost reports whether the bind host is loopback-only (safe default).
func isLoopbackHost(host string) bool {
	switch host {
	case "", "localhost", "127.0.0.1", "::1":
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return false
}

func init() {
	serveCmd.Flags().Int("port", 5000, "Port to listen on")
	serveCmd.Flags().String("host", "127.0.0.1", `Bind address (use "0.0.0.0" to expose on all interfaces)`)
	serveCmd.Flags().String("tls-cert", "", "TLS certificate file (enables HTTPS)")
	serveCmd.Flags().String("tls-key", "", "TLS key file")
	serveCmd.Flags().Duration("presign-ttl", time.Hour, "TTL for S3 presigned blob URLs")
	serveCmd.Flags().Int("cache-entries", 1000, "Manifests to keep cached in memory (0 = unlimited)")
	serveCmd.Flags().Duration("cache-ttl", 5*time.Minute, "How long a cached manifest stays valid")
	rootCmd.AddCommand(serveCmd)
}
