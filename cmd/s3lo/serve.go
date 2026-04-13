package main

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/OuFinx/s3lo/pkg/image"
	"github.com/OuFinx/s3lo/pkg/serve"
	storage "github.com/OuFinx/s3lo/pkg/storage"
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

		bucket, _, err := image.ParseConfigRef(args[0])
		if err != nil {
			return err
		}

		client, err := storage.NewBackendFromRef(cmd.Context(), args[0])
		if err != nil {
			return err
		}

		srv := &serve.Server{
			Client:     client,
			Bucket:     bucket,
			PresignTTL: presignTTL,
		}

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
		fmt.Printf("Press Ctrl+C to stop.\n\n")

		if tlsCert != "" {
			return http.ListenAndServeTLS(addr, tlsCert, tlsKey, srv)
		}
		return http.ListenAndServe(addr, srv)
	},
}

func init() {
	serveCmd.Flags().Int("port", 5000, "Port to listen on")
	serveCmd.Flags().String("host", "127.0.0.1", `Bind address (use "0.0.0.0" to expose on all interfaces)`)
	serveCmd.Flags().String("tls-cert", "", "TLS certificate file (enables HTTPS)")
	serveCmd.Flags().String("tls-key", "", "TLS key file")
	serveCmd.Flags().Duration("presign-ttl", time.Hour, "TTL for S3 presigned blob URLs")
	rootCmd.AddCommand(serveCmd)
}
