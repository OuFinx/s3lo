package main

import (
	"errors"
	"testing"
)

func TestIsUsageError(t *testing.T) {
	usage := []error{
		errors.New(`unknown command "frobnicate" for "s3lo"`),
		errors.New("unknown flag: --nope"),
		errors.New("unknown shorthand flag: 'x' in -x"),
		errors.New("flag needs an argument: --key"),
		errors.New(`invalid argument "abc" for "--count" flag: strconv.Atoi: parsing "abc"`),
		errors.New("accepts 1 arg(s), received 0"),
		errors.New("requires at least 1 arg(s), only received 0"),
	}
	for _, e := range usage {
		if !isUsageError(e) {
			t.Errorf("expected usage error: %v", e)
		}
	}

	runtime := []error{
		errors.New("verify layer blob: digest mismatch"),
		errors.New("tag v1.0 is immutable and cannot be deleted"),
		errors.New("download config blob: connection refused"),
		errors.New("image s3://b/app:v1 not found"),
	}
	for _, e := range runtime {
		if isUsageError(e) {
			t.Errorf("runtime error must not be a usage error: %v", e)
		}
	}
}
