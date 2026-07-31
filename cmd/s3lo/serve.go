package main

import (
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/OuFinx/s3lo/v3/pkg/image"
	"github.com/OuFinx/s3lo/v3/pkg/serve"
	storage "github.com/OuFinx/s3lo/v3/pkg/storage"
	"github.com/spf13/cobra"
)

// Compile-time check: *storage.Client must satisfy serve.Presigner.
var _ serve.Presigner = (*storage.Client)(nil)

// maxPresignTTL is the SigV4 ceiling for a presigned URL. A longer TTL is not
// clamped by S3, it is rejected at request time, so the URLs this server hands
// out would fail on arrival with no hint as to why.
const maxPresignTTL = 7 * 24 * time.Hour

var serveCmd = &cobra.Command{
	Use:   "serve <s3-ref>",
	Short: "Serve images via OCI Distribution Spec (docker pull compatible)",
	Long: `Start an HTTP server that speaks the OCI Distribution Spec,
serving images stored in the given bucket.

Enables docker pull, kubectl, and any OCI client to pull images
directly from S3 without running s3lo pull first.

For S3 and S3-compatible backends, blob requests are served via
presigned URL redirects — no data passes through this server.
For GCS, Azure, and local backends, blobs are streamed.

The server has NO authentication: anyone who can reach the port can read
every image in the bucket. It therefore binds loopback by default, and a
non-loopback --host must be confirmed with --allow-anonymous and fronted by
a firewall, a security group or an authenticating proxy. Use --tls-cert and
--tls-key (both, or neither) so pulls are not sent in the clear, and
--verify-key so unsigned images are refused.`,
	Example: `  Docs: https://oufinx.github.io/s3lo/commands/serve/

  # Serve from S3 on localhost
  s3lo serve s3://my-bucket/ --port 5000

  # Pull from it with Docker
  docker pull localhost:5000/myapp:v1.0

  # Expose on all interfaces: unauthenticated, so TLS and an explicit opt-in
  s3lo serve s3://my-bucket/ --host 0.0.0.0 --allow-anonymous \
    --tls-cert cert.pem --tls-key key.pem

  # Refuse to serve images that are not signed by this key
  s3lo serve s3://my-bucket/ --verify-key cosign.pub

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
		allowAnonymous, _ := cmd.Flags().GetBool("allow-anonymous")
		verifyKey, _ := cmd.Flags().GetString("verify-key")
		maxConcurrent, _ := cmd.Flags().GetInt("max-concurrent")

		if presignTTL <= 0 || presignTTL > maxPresignTTL {
			return fmt.Errorf("invalid --presign-ttl %s: must be greater than 0 and at most %s (the SigV4 maximum; longer URLs are rejected by S3)",
				presignTTL, maxPresignTTL)
		}
		// Keying the scheme on tls-cert alone meant --tls-key on its own served
		// plaintext HTTP while looking configured for HTTPS.
		if tlsKey == "" && tlsCert != "" {
			return fmt.Errorf("--tls-cert given without --tls-key: pass both to enable HTTPS, or neither")
		}
		if tlsCert == "" && tlsKey != "" {
			return fmt.Errorf("--tls-key given without --tls-cert: pass both to enable HTTPS, or neither")
		}

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

		// serve has no authentication, so a non-loopback bind publishes every image
		// in the bucket to everyone who can reach the port. That has to be asked
		// for, not stumbled into.
		if !isLoopbackHost(host) && !allowAnonymous {
			return fmt.Errorf("refusing to bind %q: serve has no authentication, so every image in %s would be readable by anyone who can reach this port; pass --allow-anonymous to accept that (and restrict access with a firewall, a security group or an authenticating proxy), or bind 127.0.0.1",
				host, bucket)
		}

		client, err := storage.NewBackendFromRef(cmd.Context(), args[0])
		if err != nil {
			return err
		}

		var verifier *serve.Verifier
		if verifyKey != "" {
			verifier, err = serve.NewVerifier(cmd.Context(), verifyKey)
			if err != nil {
				return err
			}
		}

		srv := serve.NewServer(serve.Server{
			Client:     client,
			Bucket:     bucket,
			PresignTTL: presignTTL,
			// Manifests are re-read on every pull and are kilobytes each, so a small
			// cache removes most of the request traffic at negligible memory cost.
			Cache:                serve.NewCache(cacheEntries, cacheTTL),
			Verifier:             verifier,
			MaxConcurrentStorage: maxConcurrent,
			// Without this /readyz answers 200 whether or not the bucket is
			// reachable, which is the one thing it exists to tell you.
			HealthBucket: bucket,
		})

		addr := fmt.Sprintf("%s:%d", host, port)
		scheme := "http"
		var tlsConfig *tls.Config
		if tlsCert != "" {
			// Loaded here rather than inside ListenAndServeTLS so a bad certificate
			// fails before anything is printed or bound.
			cert, err := tls.LoadX509KeyPair(tlsCert, tlsKey)
			if err != nil {
				return fmt.Errorf("load TLS keypair: %w", err)
			}
			tlsConfig = &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}
			scheme = "https"
		}

		// Bind before the banner: a taken port used to print "Serving ..." and only
		// then fail, and --port 0 printed ":0" instead of the port it got.
		ln, err := net.Listen("tcp", addr)
		if err != nil {
			return err
		}
		defer ln.Close()

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

		// The port comes from the listener, not the flag, so --port 0 reports the
		// port it actually got instead of "0".
		fmt.Printf("Serving %s at %s://%s:%d\n", ref, scheme, host, ln.Addr().(*net.TCPAddr).Port)
		fmt.Printf("Blob strategy: %s\n", blobStrategy)
		if verifier != nil {
			fmt.Printf("Signature policy: only images signed by %s are served\n", verifyKey)
		}

		if !isLoopbackHost(host) {
			fmt.Printf("\n⚠️  WARNING: binding to %q with no authentication — every image in the bucket is\n", host)
			fmt.Printf("   readable by anyone who can reach this port. Restrict access at the network layer\n")
			fmt.Printf("   (firewall/security group) or put an authenticating proxy in front.\n")
			if tlsConfig == nil {
				fmt.Printf("   Traffic is also unencrypted: pass --tls-cert and --tls-key.\n")
			}
		}
		fmt.Printf("Press Ctrl+C to stop.\n\n")

		httpSrv := &http.Server{
			Handler: serve.LogRequests(srv),
			// A zero-value server has no deadlines at all, so one slow client holding
			// a half-sent request header parks a connection for as long as it likes.
			ReadHeaderTimeout: 10 * time.Second,
			ReadTimeout:       30 * time.Second,
			// ponytail: one flat write deadline. Reads are GET/HEAD, and the slow
			// case is streaming a whole layer from a non-presigning backend, so this
			// is sized for that rather than for the common redirect. Make it a flag
			// if anyone actually serves layers that take longer than this.
			WriteTimeout: 10 * time.Minute,
			IdleTimeout:  120 * time.Second,
			TLSConfig:    tlsConfig,
		}

		if tlsConfig != nil {
			return httpSrv.ServeTLS(ln, "", "")
		}
		return httpSrv.Serve(ln)
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
	serveCmd.Flags().String("host", "127.0.0.1", `Bind address (use "0.0.0.0" with --allow-anonymous to expose on all interfaces)`)
	serveCmd.Flags().Bool("allow-anonymous", false, "Required to bind a non-loopback address: confirms unauthenticated access is intended")
	serveCmd.Flags().String("tls-cert", "", "TLS certificate file (enables HTTPS; requires --tls-key)")
	serveCmd.Flags().String("tls-key", "", "TLS key file (requires --tls-cert)")
	serveCmd.Flags().String("verify-key", "", "Verification key (.pub file, awskms://, hashivault://): serve only images signed by it")
	serveCmd.Flags().Duration("presign-ttl", 15*time.Minute, "TTL for S3 presigned blob URLs (max 168h)")
	serveCmd.Flags().Int("cache-entries", 1000, "Manifests to keep cached in memory (0 = unlimited)")
	serveCmd.Flags().Duration("cache-ttl", 5*time.Minute, "How long a cached manifest stays valid")
	serveCmd.Flags().Int("max-concurrent", 64, "Maximum concurrent object-storage operations (0 = unlimited)")
	rootCmd.AddCommand(serveCmd)
}
