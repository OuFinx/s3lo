package image

import (
	"context"
	"fmt"

	"github.com/OuFinx/s3lo/pkg/oci"
	storage "github.com/OuFinx/s3lo/pkg/storage"
)

// collectManifestReferences returns every blob digest referenced by a top-level
// manifest document. It understands both OCI manifests and OCI indexes.
//
// The returned map uses digests without the "sha256:" prefix to match the blob
// storage layout under blobs/sha256/<digest>.
func collectManifestReferences(ctx context.Context, client storage.Backend, bucket string, manifestData []byte) (map[string]struct{}, error) {
	referenced := make(map[string]struct{})
	visitedManifests := make(map[string]struct{})

	var walk func([]byte) error
	walk = func(data []byte) error {
		if isImageIndex(data) {
			idx, err := parseIndex(data)
			if err != nil {
				return fmt.Errorf("parse image index: %w", err)
			}
			for _, desc := range idx.Manifests {
				digest := desc.Digest.Encoded()
				if digest == "" {
					continue
				}
				referenced[digest] = struct{}{}
				if _, ok := visitedManifests[digest]; ok {
					continue
				}
				visitedManifests[digest] = struct{}{}

				childData, err := client.GetObject(ctx, bucket, "blobs/sha256/"+digest)
				if err != nil {
					return fmt.Errorf("fetch manifest blob %s: %w", desc.Digest.String(), err)
				}
				if err := walk(childData); err != nil {
					return err
				}
			}
			return nil
		}

		manifest, err := oci.ParseManifest(data)
		if err != nil {
			return fmt.Errorf("parse manifest: %w", err)
		}

		if digest := manifest.Config.Digest.Encoded(); digest != "" {
			referenced[digest] = struct{}{}
		}
		for _, layer := range manifest.Layers {
			if digest := layer.Digest.Encoded(); digest != "" {
				referenced[digest] = struct{}{}
			}
		}
		return nil
	}

	if err := walk(manifestData); err != nil {
		return referenced, err
	}
	return referenced, nil
}
