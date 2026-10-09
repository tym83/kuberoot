package s3put

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

// The "PUT Object" example of the AWS Signature Version 4 documentation for S3.
func TestSignatureMatchesTheAWSExample(t *testing.T) {
	req, _ := http.NewRequest(http.MethodPut, "https://examplebucket.s3.amazonaws.com/test$file.text", strings.NewReader("Welcome to Amazon S3."))
	req.Header.Set("Date", "Fri, 24 May 2013 00:00:00 GMT")
	req.Header.Set("x-amz-storage-class", "REDUCED_REDUNDANCY")
	target := Target{Endpoint: &url.URL{Scheme: "https", Host: "examplebucket.s3.amazonaws.com"}, Region: "us-east-1",
		AccessKey: "AKIAIOSFODNN7EXAMPLE", SecretKey: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"}
	target.sign(req, "44ce7dd67c959e0d3524ffac1771dfbba87d2b6b4b4e99e42034a8b803f8b072", time.Date(2013, 5, 24, 0, 0, 0, 0, time.UTC))
	want := "AWS4-HMAC-SHA256 Credential=AKIAIOSFODNN7EXAMPLE/20130524/us-east-1/s3/aws4_request," +
		"SignedHeaders=date;host;x-amz-content-sha256;x-amz-date;x-amz-storage-class," +
		"Signature=98ad721746da40c64f1a55b78f14c238d841ea1380cd77a1b5971af0ece108bd"
	if got := req.Header.Get("Authorization"); got != want {
		t.Errorf("Authorization =\n%s\nwant\n%s", got, want)
	}
}

func TestEncodePathKeepsSlashesAndUnreserved(t *testing.T) {
	if got := encodePath("/backups/kuberoot/a b+c~d.tar.gz"); got != "/backups/kuberoot/a%20b%2Bc~d.tar.gz" {
		t.Errorf("encodePath = %s", got)
	}
}
