package main

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/OuFinx/s3lo/v2/pkg/image"
	storage "github.com/OuFinx/s3lo/v2/pkg/storage"
	"github.com/spf13/cobra"
)

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Manage bucket and image configuration",
}

// config set s3://bucket/ key=val [key=val ...]
// config set s3://bucket/myapp key=val [key=val ...]
var configSetCmd = &cobra.Command{
	Use:   "set <s3-ref> <key>=<value> [<key>=<value> ...]",
	Short: "Set configuration for a bucket or image",
	Long: `Set configuration values stored in s3lo.yaml at the bucket root.

Use s3://bucket/ to set bucket defaults (apply to all images).
Use s3://bucket/image or s3://bucket/dev/* to set per-image overrides.

Available keys:
  immutable              true/false
  chunked                true|false, default true (bucket-wide: store layers as shared chunks)
  lifecycle              the whole lifecycle block (unset only)
  lifecycle.keep_last    number (e.g. 10)
  lifecycle.max_age      duration (e.g. 30d, 7d, 168h)
  lifecycle.keep_tags    comma-separated tags (e.g. latest,stable)

An empty value unsets the key, at bucket or image scope: "immutable=" removes
the setting rather than storing a value.`,
	Example: `  Docs: https://oufinx.github.io/s3lo/commands/config/

  # Bucket defaults
  s3lo config set s3://my-bucket/ immutable=false lifecycle.keep_last=10 lifecycle.max_age=90d

  # Per-image
  s3lo config set s3://my-bucket/myapp immutable=true lifecycle.keep_last=5 lifecycle.keep_tags=stable,latest
  s3lo config set s3://my-bucket/dev/* lifecycle.max_age=7d lifecycle.keep_tags=latest

  # Unset with an empty value
  s3lo config set s3://my-bucket/myapp immutable=          # drop the image override
  s3lo config set s3://my-bucket/myapp lifecycle=          # drop the whole lifecycle block
  s3lo config set s3://my-bucket/ chunked=                 # drop the bucket-wide setting`,
	Args: cobra.MinimumNArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		bucket, imageName, err := image.ParseConfigRef(args[0])
		if err != nil {
			return err
		}

		client, err := storage.NewBackendFromRef(cmd.Context(), args[0])
		if err != nil {
			return err
		}

		cfg, err := image.GetBucketConfig(cmd.Context(), client, bucket)
		if err != nil {
			return err
		}

		// Apply each key=value pair to the appropriate section.
		for _, kv := range args[1:] {
			parts := strings.SplitN(kv, "=", 2)
			if len(parts) != 2 {
				return fmt.Errorf("invalid key=value format: %q (expected key=value)", kv)
			}
			if err := applyConfigKV(cfg, imageName, parts[0], parts[1]); err != nil {
				return fmt.Errorf("%s: %w", kv, err)
			}
		}

		if err := image.SetBucketConfig(cmd.Context(), client, bucket, cfg); err != nil {
			return err
		}

		scheme := refScheme(args[0])
		target := scheme + bucket + "/"
		if imageName != "" {
			target = scheme + bucket + "/" + imageName
		}
		fmt.Printf("Config updated for %s\n", target)
		return nil
	},
}

// config get s3://bucket/
// config get s3://bucket/myapp
var configGetCmd = &cobra.Command{
	Use:   "get <s3-ref>",
	Short: "Show configuration for a bucket or image",
	Example: `  Docs: https://oufinx.github.io/s3lo/commands/config/

  s3lo config get s3://my-bucket/           # show all configs
  s3lo config get s3://my-bucket/myapp      # show effective config for an image
  s3lo config get s3://my-bucket/ --output json`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		outputFmt, _ := cmd.Flags().GetString("output")
		bucket, imageName, err := image.ParseConfigRef(args[0])
		if err != nil {
			return err
		}

		client, err := storage.NewBackendFromRef(cmd.Context(), args[0])
		if err != nil {
			return err
		}

		cfg, err := image.GetBucketConfig(cmd.Context(), client, bucket)
		if err != nil {
			return err
		}

		if outputFmt != "" && outputFmt != "table" {
			if imageName != "" {
				eff := cfg.EffectiveConfig(imageName)
				ok, err := writeOutput(outputFmt, eff)
				if err != nil {
					return err
				}
				if ok {
					return nil
				}
			} else {
				ok, err := writeOutput(outputFmt, cfg)
				if err != nil {
					return err
				}
				if ok {
					return nil
				}
			}
		}

		scheme := refScheme(args[0])
		if imageName == "" {
			printBucketConfig(scheme, bucket, cfg)
		} else {
			printImageConfig(scheme, bucket, imageName, cfg)
		}
		return nil
	},
}

// applyConfigKV applies a single key=value pair to the config for the given image name
// (empty imageName = bucket default). An empty value unsets the key.
func applyConfigKV(cfg *image.BucketConfig, imageName, key, val string) error {
	// Chunking applies to the whole bucket: the chunk store is shared, so there is
	// no coherent meaning to enabling it for one image only.
	if key == "chunked" {
		if imageName != "" {
			return fmt.Errorf("chunked is a bucket-wide setting: set it on the bucket reference, not on image %q", imageName)
		}
		if val == "" {
			cfg.Chunked = nil
			return nil
		}
		b, err := strconv.ParseBool(val)
		if err != nil {
			return fmt.Errorf("chunked must be true or false, got %q", val)
		}
		cfg.Chunked = &b
		return nil
	}
	if imageName == "" {
		return applyToImageConfig(&cfg.Default, key, val)
	}
	if cfg.Images == nil {
		cfg.Images = make(map[string]image.ImageConfig)
	}
	img := cfg.Images[imageName]
	if err := applyToImageConfig(&img, key, val); err != nil {
		return err
	}
	// An image entry with nothing left in it is noise in s3lo.yaml.
	if img.Immutable == nil && img.Lifecycle == nil {
		delete(cfg.Images, imageName)
		return nil
	}
	cfg.Images[imageName] = img
	return nil
}

func applyToImageConfig(img *image.ImageConfig, key, val string) error {
	// An empty value means "unset", so `config set` is reversible at both
	// bucket and image scope.
	if val == "" {
		switch key {
		case "immutable":
			img.Immutable = nil
		case "lifecycle":
			img.Lifecycle = nil
		case "lifecycle.keep_last":
			clearLifecycle(img, func(lc *image.LifecycleImageConfig) { lc.KeepLast = 0 })
		case "lifecycle.max_age":
			clearLifecycle(img, func(lc *image.LifecycleImageConfig) { lc.MaxAge = "" })
		case "lifecycle.keep_tags":
			clearLifecycle(img, func(lc *image.LifecycleImageConfig) { lc.KeepTags = nil })
		default:
			return fmt.Errorf("unknown key %q (valid keys: chunked, immutable, lifecycle, lifecycle.keep_last, lifecycle.max_age, lifecycle.keep_tags)", key)
		}
		return nil
	}
	switch key {
	case "lifecycle":
		return fmt.Errorf("lifecycle is a block, not a value: use lifecycle.keep_last / lifecycle.max_age / lifecycle.keep_tags, or lifecycle= to unset it")
	case "immutable":
		b, err := strconv.ParseBool(val)
		if err != nil {
			return fmt.Errorf("immutable must be true or false, got %q", val)
		}
		img.Immutable = &b
	case "lifecycle.keep_last":
		n, err := strconv.Atoi(val)
		if err != nil || n < 0 {
			return fmt.Errorf("lifecycle.keep_last must be a non-negative integer, got %q", val)
		}
		if img.Lifecycle == nil {
			img.Lifecycle = &image.LifecycleImageConfig{}
		}
		img.Lifecycle.KeepLast = n
	case "lifecycle.max_age":
		if _, err := image.ParseDuration(val); err != nil {
			return fmt.Errorf("lifecycle.max_age must be a duration like 30d or 168h, got %q", val)
		}
		if img.Lifecycle == nil {
			img.Lifecycle = &image.LifecycleImageConfig{}
		}
		img.Lifecycle.MaxAge = val
	case "lifecycle.keep_tags":
		tags := strings.Split(val, ",")
		for i := range tags {
			tags[i] = strings.TrimSpace(tags[i])
		}
		if img.Lifecycle == nil {
			img.Lifecycle = &image.LifecycleImageConfig{}
		}
		img.Lifecycle.KeepTags = tags
	default:
		return fmt.Errorf("unknown key %q (valid keys: chunked, immutable, lifecycle.keep_last, lifecycle.max_age, lifecycle.keep_tags)", key)
	}
	return nil
}

// clearLifecycle drops one lifecycle field, and the whole block once nothing is
// left in it — an all-zero lifecycle would otherwise linger in s3lo.yaml.
func clearLifecycle(img *image.ImageConfig, clear func(*image.LifecycleImageConfig)) {
	if img.Lifecycle == nil {
		return
	}
	clear(img.Lifecycle)
	if img.Lifecycle.KeepLast == 0 && img.Lifecycle.MaxAge == "" && len(img.Lifecycle.KeepTags) == 0 {
		img.Lifecycle = nil
	}
}

// --- output formatting ---

func printBucketConfig(scheme, bucket string, cfg *image.BucketConfig) {
	fmt.Printf("Bucket: %s%s/\n", scheme, bucket)

	fmt.Println("\nDefault:")
	printImageConfigFields(cfg.Default, "  ")

	if len(cfg.Images) > 0 {
		fmt.Println("\nImages:")
		for name, img := range cfg.Images {
			fmt.Printf("  %s\n", name)
			printImageConfigFields(img, "    ")
		}
	}
}

func printImageConfig(scheme, bucket, imageName string, cfg *image.BucketConfig) {
	fmt.Printf("Image: %s (%s%s/)\n", imageName, scheme, bucket)
	eff := cfg.EffectiveConfig(imageName)
	imgOverride, hasOverride := cfg.Images[imageName]

	fmt.Println()
	sourceFor := func(field string) string {
		switch field {
		case "immutable":
			if hasOverride && imgOverride.Immutable != nil {
				return "[image]"
			}
		case "lifecycle":
			if hasOverride && imgOverride.Lifecycle != nil {
				return "[image]"
			}
		}
		return "[default]"
	}

	if eff.Immutable != nil {
		fmt.Printf("  %-30s %v  %s\n", "immutable:", *eff.Immutable, sourceFor("immutable"))
	}
	if eff.Lifecycle != nil {
		lc := eff.Lifecycle
		src := sourceFor("lifecycle")
		if lc.KeepLast > 0 {
			fmt.Printf("  %-30s %d  %s\n", "lifecycle.keep_last:", lc.KeepLast, src)
		}
		if lc.MaxAge != "" {
			fmt.Printf("  %-30s %s  %s\n", "lifecycle.max_age:", lc.MaxAge, src)
		}
		if len(lc.KeepTags) > 0 {
			fmt.Printf("  %-30s %s  %s\n", "lifecycle.keep_tags:", strings.Join(lc.KeepTags, ", "), src)
		}
	}
	if eff.Immutable == nil && eff.Lifecycle == nil {
		fmt.Println("  (no configuration set)")
	}
}

func printImageConfigFields(img image.ImageConfig, indent string) {
	if img.Immutable == nil && img.Lifecycle == nil {
		fmt.Printf("%s(none)\n", indent)
		return
	}
	if img.Immutable != nil {
		fmt.Printf("%s%-30s %v\n", indent, "immutable:", *img.Immutable)
	}
	if img.Lifecycle != nil {
		lc := img.Lifecycle
		if lc.KeepLast > 0 {
			fmt.Printf("%s%-30s %d\n", indent, "lifecycle.keep_last:", lc.KeepLast)
		}
		if lc.MaxAge != "" {
			fmt.Printf("%s%-30s %s\n", indent, "lifecycle.max_age:", lc.MaxAge)
		}
		if len(lc.KeepTags) > 0 {
			fmt.Printf("%s%-30s %s\n", indent, "lifecycle.keep_tags:", strings.Join(lc.KeepTags, ", "))
		}
	}
}

func refScheme(rawRef string) string {
	switch {
	case strings.HasPrefix(rawRef, "gs://"):
		return "gs://"
	case strings.HasPrefix(rawRef, "az://"):
		return "az://"
	case strings.HasPrefix(rawRef, "local://"):
		return "local://"
	default:
		return "s3://"
	}
}

func init() {
	configGetCmd.Flags().StringP("output", "o", "", "Output format: json, yaml, or table (default)")
	configCmd.AddCommand(configSetCmd)
	configCmd.AddCommand(configGetCmd)
	rootCmd.AddCommand(configCmd)
}
