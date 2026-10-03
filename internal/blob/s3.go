package blob

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// S3 is an S3-compatible BlobStore (AWS S3, MinIO, R2, …) for scale tiers. It
// speaks the REST API directly with AWS Signature V4 — no AWS SDK dependency, so
// the binary stays small and pure-Go. Path-style addressing is used (works with
// MinIO and S3 alike). All objects live under one configured bucket, keyed by the
// opaque storage key (object UUID), exactly mirroring the local adapter.
type S3 struct {
	endpoint  string // e.g. https://s3.us-east-1.amazonaws.com or http://localhost:9000
	region    string
	bucket    string
	accessKey string
	secretKey string
	http      *http.Client
}

// S3Config configures an S3 adapter.
type S3Config struct {
	Endpoint  string
	Region    string
	Bucket    string
	AccessKey string
	SecretKey string
}

// NewS3 validates config and returns an S3 store.
func NewS3(c S3Config) (*S3, error) {
	if c.Endpoint == "" || c.Bucket == "" || c.AccessKey == "" || c.SecretKey == "" {
		return nil, fmt.Errorf("s3: endpoint, bucket, access key, and secret key are all required")
	}
	if c.Region == "" {
		c.Region = "us-east-1"
	}
	if _, err := url.Parse(c.Endpoint); err != nil {
		return nil, fmt.Errorf("s3: invalid endpoint: %w", err)
	}
	return &S3{
		endpoint:  strings.TrimRight(c.Endpoint, "/"),
		region:    c.Region,
		bucket:    c.Bucket,
		accessKey: c.AccessKey,
		secretKey: c.SecretKey,
		http:      &http.Client{Timeout: 60 * time.Second},
	}, nil
}

func (s *S3) Kind() string { return "s3" }

func (s *S3) objectURL(key string) string {
	return s.endpoint + "/" + s.bucket + "/" + s3URIEncode(key, false)
}

func (s *S3) Put(ctx context.Context, key string, r io.Reader) (int64, string, error) {
	// SigV4 header signing needs the exact payload hash up front, and we also need
	// the sha256 as our etag, so the body is buffered once here. Objects are
	// size-capped at the HTTP layer (PS_STORAGE_MAX_OBJECT_BYTES), which bounds this
	// allocation. (A streaming SigV4 chunked upload would remove the buffer; it's a
	// known scale limitation, not a correctness issue.) The local adapter streams.
	buf, err := io.ReadAll(r)
	if err != nil {
		return 0, "", err
	}
	sum := sha256.Sum256(buf)
	etag := hex.EncodeToString(sum[:])

	// bytes.NewReader wraps the buffer without copying (unlike strings.NewReader(string(buf))).
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, s.objectURL(key), bytes.NewReader(buf))
	if err != nil {
		return 0, "", err
	}
	req.ContentLength = int64(len(buf))
	s.sign(req, hex.EncodeToString(sum[:]))

	resp, err := s.http.Do(req)
	if err != nil {
		return 0, "", err
	}
	defer drain(resp.Body)
	if resp.StatusCode/100 != 2 {
		return 0, "", s3Error("PUT", resp)
	}
	return int64(len(buf)), etag, nil
}

func (s *S3) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.objectURL(key), nil)
	if err != nil {
		return nil, err
	}
	s.sign(req, emptyPayloadHash)
	resp, err := s.http.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusNotFound {
		drain(resp.Body)
		return nil, ErrNotFound
	}
	if resp.StatusCode/100 != 2 {
		defer drain(resp.Body)
		return nil, s3Error("GET", resp)
	}
	return resp.Body, nil
}

func (s *S3) Delete(ctx context.Context, key string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, s.objectURL(key), nil)
	if err != nil {
		return err
	}
	s.sign(req, emptyPayloadHash)
	resp, err := s.http.Do(req)
	if err != nil {
		return err
	}
	defer drain(resp.Body)
	// S3 returns 204 on delete; it does not 404 for a missing key.
	if resp.StatusCode/100 != 2 {
		return s3Error("DELETE", resp)
	}
	return nil
}

const emptyPayloadHash = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855" // sha256("")

func drain(rc io.ReadCloser) { _, _ = io.Copy(io.Discard, io.LimitReader(rc, 1<<16)); rc.Close() }

func s3Error(op string, resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
	return fmt.Errorf("s3 %s: status %d: %s", op, resp.StatusCode, strings.TrimSpace(string(body)))
}

// --- AWS Signature V4 (header-based) --------------------------------------

func (s *S3) sign(req *http.Request, payloadHash string) {
	now := time.Now().UTC()
	amzDate := now.Format("20060102T150405Z")
	dateStamp := now.Format("20060102")

	req.Header.Set("Host", req.URL.Host)
	req.Header.Set("X-Amz-Date", amzDate)
	req.Header.Set("X-Amz-Content-Sha256", payloadHash)

	signedHeaders := "host;x-amz-content-sha256;x-amz-date"
	canonicalHeaders := "host:" + req.URL.Host + "\n" +
		"x-amz-content-sha256:" + payloadHash + "\n" +
		"x-amz-date:" + amzDate + "\n"

	canonicalURI := s3CanonicalPath(req.URL.Path)
	canonicalRequest := strings.Join([]string{
		req.Method,
		canonicalURI,
		req.URL.RawQuery,
		canonicalHeaders,
		signedHeaders,
		payloadHash,
	}, "\n")

	scope := dateStamp + "/" + s.region + "/s3/aws4_request"
	stringToSign := strings.Join([]string{
		"AWS4-HMAC-SHA256",
		amzDate,
		scope,
		hashHex([]byte(canonicalRequest)),
	}, "\n")

	signingKey := deriveSigningKey(s.secretKey, dateStamp, s.region, "s3")
	signature := hex.EncodeToString(hmacSHA256(signingKey, []byte(stringToSign)))

	auth := "AWS4-HMAC-SHA256 " +
		"Credential=" + s.accessKey + "/" + scope + ", " +
		"SignedHeaders=" + signedHeaders + ", " +
		"Signature=" + signature
	req.Header.Set("Authorization", auth)
}

func deriveSigningKey(secret, dateStamp, region, service string) []byte {
	kDate := hmacSHA256([]byte("AWS4"+secret), []byte(dateStamp))
	kRegion := hmacSHA256(kDate, []byte(region))
	kService := hmacSHA256(kRegion, []byte(service))
	return hmacSHA256(kService, []byte("aws4_request"))
}

func hmacSHA256(key, data []byte) []byte {
	h := hmac.New(sha256.New, key)
	h.Write(data)
	return h.Sum(nil)
}

func hashHex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// s3CanonicalPath re-encodes each path segment per SigV4 rules (the path already
// contains a URI-encoded key from objectURL; decode-then-reencode keeps it stable).
func s3CanonicalPath(p string) string {
	segs := strings.Split(p, "/")
	for i, seg := range segs {
		dec, err := url.PathUnescape(seg)
		if err != nil {
			dec = seg
		}
		segs[i] = s3URIEncode(dec, true)
	}
	return strings.Join(segs, "/")
}

// s3URIEncode encodes per AWS rules: unreserved chars pass through; everything
// else is %XX. When encodeSlash is false, '/' is preserved (used for the key in
// the request URL); when true, '/' is encoded (used inside a single path segment).
func s3URIEncode(s string, encodeSlash bool) string {
	const unreserved = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-._~"
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case strings.IndexByte(unreserved, c) >= 0:
			b.WriteByte(c)
		case c == '/' && !encodeSlash:
			b.WriteByte('/')
		default:
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}
