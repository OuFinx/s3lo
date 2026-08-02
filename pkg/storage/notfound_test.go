package storage

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	smithy "github.com/aws/smithy-go"
	smithyhttp "github.com/aws/smithy-go/transport/http"
)

// respErr builds the shape the AWS SDK wraps an HTTP failure in.
func respErr(status int, code, msg string) error {
	return &smithyhttp.ResponseError{
		Response: &smithyhttp.Response{Response: &http.Response{StatusCode: status}},
		Err:      &smithy.GenericAPIError{Code: code, Message: msg},
	}
}

// TestS3NotFound_DoesNotGuessFromMessageText pins the fix for a real defect:
// existence was decided by looking for "404" or "NotFound" anywhere in the
// error text. AWS puts the object key into that text, and roughly one sha256
// digest in sixty-six contains "404", so a throttle or an AccessDenied on such
// a key silently became "the object is not there".
//
// Callers act on that answer: push skips a re-upload, and the immutability
// check concludes the tag is free to overwrite.
func TestS3NotFound_DoesNotGuessFromMessageText(t *testing.T) {
	// A digest that happens to contain "404". Nothing unusual about it.
	const key = `arn:aws:s3:::my-bucket/blobs/sha256/9f2404e1b7c8d3a5f60418ba9c7d2e5f1a3b6c8d9e0f1a2b3c4d5e6f708192a3`

	notFound := []struct {
		name string
		err  error
	}{
		{"404 response", respErr(http.StatusNotFound, "NotFound", "Not Found")},
		{"NoSuchKey code", respErr(http.StatusNotFound, "NoSuchKey", "The specified key does not exist.")},
		{"wrapped 404", fmt.Errorf("head object: %w", respErr(http.StatusNotFound, "NotFound", "Not Found"))},
	}
	for _, tc := range notFound {
		if !s3NotFound(tc.err) {
			t.Errorf("%s: want not-found, got false", tc.name)
		}
	}

	present := []struct {
		name string
		err  error
	}{
		// The ones that used to be misread. Each is a genuine failure whose
		// message carries a key containing "404".
		{"AccessDenied on a key containing 404", respErr(http.StatusForbidden, "AccessDenied",
			"User is not authorized to perform: s3:GetObject on resource: "+key)},
		{"throttling on a key containing 404", respErr(http.StatusServiceUnavailable, "SlowDown",
			"Please reduce your request rate: "+key)},
		{"internal error on a key containing 404", respErr(http.StatusInternalServerError, "InternalError",
			"We encountered an internal error: "+key)},
		// A plain transport failure is not evidence the object is absent.
		{"network error", errors.New("dial tcp: lookup s3.amazonaws.com: no such host")},
		{"context deadline", context_DeadlineExceeded()},
	}
	for _, tc := range present {
		if s3NotFound(tc.err) {
			t.Errorf("%s: reported as not-found, but nothing proved the object is absent", tc.name)
		}
	}

	if s3NotFound(nil) {
		t.Error("nil error reported as not-found")
	}
}

func context_DeadlineExceeded() error {
	return fmt.Errorf("operation error S3: HeadObject, context deadline exceeded")
}
