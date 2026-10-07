// Package s3put uploads a file to S3-compatible object storage with a single
// PUT signed with AWS Signature Version 4: all a node needs to ship backups.
package s3put

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"
)

// Target is a bucket reached by path-style URLs: endpoint/bucket/key.
type Target struct {
	Endpoint  *url.URL
	Region    string
	Bucket    string
	AccessKey string
	SecretKey string
}

// Put uploads the file at path as key; payloadSHA256 is its hex SHA-256.
func (t Target) Put(ctx context.Context, client *http.Client, key, path, payloadSHA256 string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	u := *t.Endpoint
	u.Path = strings.TrimSuffix(u.Path, "/") + "/" + t.Bucket + "/" + key
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, u.String(), f)
	if err != nil {
		return err
	}
	req.ContentLength = info.Size()
	req.Header.Set("Content-Type", "application/gzip")
	t.sign(req, payloadSHA256, time.Now())
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("PUT %s/%s: %s: %s", t.Bucket, key, resp.Status, strings.TrimSpace(string(body)))
	}
	return nil
}

// sign adds the date, payload hash and Authorization headers. Host and every
// x-amz-* header are signed, as are Content-Type, Date and Range when present.
func (t Target) sign(req *http.Request, payloadSHA256 string, now time.Time) {
	stamp := now.UTC().Format("20060102T150405Z")
	day := stamp[:8]
	req.Header.Set("x-amz-date", stamp)
	req.Header.Set("x-amz-content-sha256", payloadSHA256)

	headers := map[string]string{"host": req.URL.Host}
	for name, values := range req.Header {
		l := strings.ToLower(name)
		if strings.HasPrefix(l, "x-amz-") || l == "content-type" || l == "date" || l == "range" {
			headers[l] = strings.TrimSpace(strings.Join(values, ","))
		}
	}
	names := make([]string, 0, len(headers))
	for n := range headers {
		names = append(names, n)
	}
	sort.Strings(names)
	var canonHeaders strings.Builder
	for _, n := range names {
		canonHeaders.WriteString(n + ":" + headers[n] + "\n")
	}
	signed := strings.Join(names, ";")

	canonical := strings.Join([]string{req.Method, encodePath(req.URL.Path), req.URL.RawQuery,
		canonHeaders.String(), signed, payloadSHA256}, "\n")
	scope := day + "/" + t.Region + "/s3/aws4_request"
	toSign := "AWS4-HMAC-SHA256\n" + stamp + "\n" + scope + "\n" + hexSum([]byte(canonical))

	key := hmacSum([]byte("AWS4"+t.SecretKey), day)
	for _, part := range []string{t.Region, "s3", "aws4_request"} {
		key = hmacSum(key, part)
	}
	signature := hex.EncodeToString(hmacSum(key, toSign))
	req.Header.Set("Authorization", fmt.Sprintf("AWS4-HMAC-SHA256 Credential=%s/%s,SignedHeaders=%s,Signature=%s",
		t.AccessKey, scope, signed, signature))
}

// encodePath escapes every byte but the unreserved ones and the slashes.
func encodePath(p string) string {
	var b strings.Builder
	for i := 0; i < len(p); i++ {
		c := p[i]
		if c == '/' || c == '-' || c == '_' || c == '.' || c == '~' ||
			('A' <= c && c <= 'Z') || ('a' <= c && c <= 'z') || ('0' <= c && c <= '9') {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

func hexSum(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

func hmacSum(key []byte, data string) []byte {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(data))
	return m.Sum(nil)
}
