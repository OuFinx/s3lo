package image

import "testing"

func TestECRRegionParsing(t *testing.T) {
	cases := []struct {
		host   string
		region string
		ok     bool
	}{
		{"123456789.dkr.ecr.us-east-1.amazonaws.com", "us-east-1", true},
		{"123456789.dkr.ecr.eu-west-2.amazonaws.com", "eu-west-2", true},
		{"123456789.dkr.ecr.us-gov-east-1.amazonaws.com", "us-gov-east-1", true},
		{"123456789.dkr.ecr.cn-north-1.amazonaws.com.cn", "cn-north-1", true},
		{"gcr.io", "", false},
		{"docker.io", "", false},
	}
	for _, tc := range cases {
		m := ecrRegionRE.FindStringSubmatch(tc.host)
		if tc.ok {
			if m == nil {
				t.Errorf("host %q: expected match, got none", tc.host)
			} else if m[1] != tc.region {
				t.Errorf("host %q: region = %q, want %q", tc.host, m[1], tc.region)
			}
		} else {
			if m != nil {
				t.Errorf("host %q: expected no match, got region %q", tc.host, m[1])
			}
		}
	}
}
