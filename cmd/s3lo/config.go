package main

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/OuFinx/s3lo/pkg/image"
	s3client "github.com/OuFinx/s3lo/pkg/s3"
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
  lifecycle.keep_last    number (e.g. 10)
  lifecycle.max_age      duration (e.g. 30d, 7d, 168h)
  lifecycle.keep_tags    comma-separated tags (e.g. latest,stable)`,
	Example: `  # Bucket defaults
  s3lo config set s3://my-bucket/ immutable=false lifecycle.keep_last=10 lifecycle.max_age=90d

  # Per-image
  s3lo config set s3://my-bucket/myapp immutable=true lifecycle.keep_last=5 lifecycle.keep_tags=stable,latest
  s3lo config set s3://my-bucket/dev/* lifecycle.max_age=7d lifecycle.keep_tags=latest`,
	Args: cobra.MinimumNArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		bucket, imageName, err := image.ParseConfigRef(args[0])
		if err != nil {
			return err
		}

		client, err := s3client.NewClient(cmd.Context())
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

		target := "s3://" + bucket + "/"
		if imageName != "" {
			target = "s3://" + bucket + "/" + imageName
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
	Example: `  s3lo config get s3://my-bucket/           # show all configs
  s3lo config get s3://my-bucket/myapp      # show effective config for an image`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		bucket, imageName, err := image.ParseConfigRef(args[0])
		if err != nil {
			return err
		}

		client, err := s3client.NewClient(cmd.Context())
		if err != nil {
			return err
		}

		cfg, err := image.GetBucketConfig(cmd.Context(), client, bucket)
		if err != nil {
			return err
		}

		if imageName == "" {
			printBucketConfig(bucket, cfg)
		} else {
			printImageConfig(bucket, imageName, cfg)
		}
		return nil
	},
}

// config remove s3://bucket/myapp
// config remove s3://bucket/myapp immutable
// config remove s3://bucket/myapp lifecycle
var configRemoveCmd = &cobra.Command{
	Use:   "remove <s3-ref> [key]",
	Short: "Remove configuration for an image",
	Long: `Remove per-image configuration overrides.

Without a key argument, removes all overrides for the image (reverts to defaults).
With a key, removes only that setting.

Valid keys to remove: immutable, lifecycle`,
	Example: `  s3lo config remove s3://my-bucket/myapp              # remove all overrides
  s3lo config remove s3://my-bucket/myapp immutable     # remove immutable override
  s3lo config remove s3://my-bucket/myapp lifecycle     # remove lifecycle override`,
	Args: cobra.RangeArgs(1, 2),
	RunE: func(cmd *cobra.Command, args []string) error {
		bucket, imageName, err := image.ParseConfigRef(args[0])
		if err != nil {
			return err
		}
		if imageName == "" {
			return fmt.Errorf("image name required (use s3://bucket/image, not s3://bucket/)")
		}

		client, err := s3client.NewClient(cmd.Context())
		if err != nil {
			return err
		}

		cfg, err := image.GetBucketConfig(cmd.Context(), client, bucket)
		if err != nil {
			return err
		}

		if cfg.Images == nil {
			fmt.Printf("No overrides found for %s\n", imageName)
			return nil
		}

		if len(args) == 1 {
			// Remove all overrides for this image.
			delete(cfg.Images, imageName)
			fmt.Printf("Removed all config overrides for %s\n", imageName)
		} else {
			key := args[1]
			img, ok := cfg.Images[imageName]
			if !ok {
				fmt.Printf("No overrides found for %s\n", imageName)
				return nil
			}
			switch key {
			case "immutable":
				img.Immutable = nil
			case "lifecycle":
				img.Lifecycle = nil
			default:
				return fmt.Errorf("unknown key %q (valid keys: immutable, lifecycle)", key)
			}
			// If no overrides remain, remove the image entry entirely.
			if img.Immutable == nil && img.Lifecycle == nil {
				delete(cfg.Images, imageName)
			} else {
				cfg.Images[imageName] = img
			}
			fmt.Printf("Removed %s override for %s\n", key, imageName)
		}

		return image.SetBucketConfig(cmd.Context(), client, bucket, cfg)
	},
}

// applyConfigKV applies a single key=value pair to the config for the given image name
// (empty imageName = bucket default).
func applyConfigKV(cfg *image.BucketConfig, imageName, key, val string) error {
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
	cfg.Images[imageName] = img
	return nil
}

func applyToImageConfig(img *image.ImageConfig, key, val string) error {
	switch key {
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
		return fmt.Errorf("unknown key %q (valid keys: immutable, lifecycle.keep_last, lifecycle.max_age, lifecycle.keep_tags)", key)
	}
	return nil
}

// --- output formatting ---

func printBucketConfig(bucket string, cfg *image.BucketConfig) {
	fmt.Printf("Bucket: s3://%s/\n", bucket)

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

func printImageConfig(bucket, imageName string, cfg *image.BucketConfig) {
	fmt.Printf("Image: %s (s3://%s/)\n", imageName, bucket)
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

var configRecommendCmd = &cobra.Command{
	Use:     "recommend <s3-bucket-ref>",
	Short:   "Show S3 bucket configuration recommendations",
	Example: `  s3lo config recommend s3://my-bucket/`,
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		result, err := image.Recommend(cmd.Context(), args[0])
		if err != nil {
			return err
		}

		fmt.Printf("Analysis for s3://%s/:\n\n", result.Bucket)
		for _, f := range result.Findings {
			status := "[good]"
			if !f.OK {
				status = "[bad] "
			}
			fmt.Printf("  %s %s\n", status, f.Label)
		}

		if len(result.Recommendations) == 0 {
			fmt.Println("\nNo recommendations — bucket looks good.")
			return nil
		}

		fmt.Printf("\nRecommendations:\n\n")
		for i, rec := range result.Recommendations {
			fmt.Printf("%d. %s\n", i+1, rec.Title)
			for _, line := range splitLines(rec.Description) {
				fmt.Printf("   %s\n", line)
			}
			fmt.Println()
		}
		return nil
	},
}

func splitLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}

func init() {
	configCmd.AddCommand(configSetCmd)
	configCmd.AddCommand(configGetCmd)
	configCmd.AddCommand(configRemoveCmd)
	configCmd.AddCommand(configRecommendCmd)
	rootCmd.AddCommand(configCmd)
}
