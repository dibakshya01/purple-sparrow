// Package storage is M6: policy-aware object storage. Object bytes live in a
// blob.Store (local filesystem or S3); this service owns buckets, object metadata,
// authorization, and presigned URLs over the data engine.
//
// Authorization model (deny-by-default, ownership-scoped — distinct from the
// record policy engine, which governs table rows):
//   - buckets are admin-managed; a bucket may be marked public (world-readable).
//   - writing requires an authenticated principal; the writer becomes the owner.
//     anon cannot write.
//   - reading an object requires: the bucket is public, OR the caller is the owner,
//     OR the caller is admin, OR the request carries a valid presigned signature.
//   - overwriting and deleting require owner or admin.
//   - listing returns the caller's own objects (admin: all in the bucket).
package storage

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/dibakshya01/purple-sparrow/internal/blob"
	"github.com/dibakshya01/purple-sparrow/internal/data"
	"github.com/dibakshya01/purple-sparrow/internal/idgen"
	"github.com/dibakshya01/purple-sparrow/internal/principal"
)

// Sentinel errors, mapped to envelope codes by the HTTP layer.
var (
	ErrBucketNotFound = errors.New("bucket not found")
	ErrBucketExists   = errors.New("bucket already exists")
	ErrObjectNotFound = errors.New("object not found")
	ErrDenied         = errors.New("access denied")
	ErrAnonWrite      = errors.New("anonymous writes are not allowed")
	ErrInvalidName    = errors.New("invalid name")
	ErrInvalidKey     = errors.New("invalid object key")
)

const (
	maxKeyLen    = 1024
	maxObjectTTL = 7 * 24 * time.Hour
)

var bucketNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{1,62}$`)

// Bucket is a logical object namespace.
type Bucket struct {
	Name      string `json:"name"`
	Public    bool   `json:"public"`
	CreatedAt string `json:"created_at"`
}

// Object is object metadata (never the bytes).
type Object struct {
	ID          string `json:"id"`
	Bucket      string `json:"bucket"`
	Key         string `json:"key"`
	Size        int64  `json:"size"`
	ContentType string `json:"content_type"`
	ETag        string `json:"etag"`
	OwnerID     string `json:"owner_id,omitempty"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

// Service owns storage metadata and authorization.
type Service struct {
	eng        data.Engine
	store      blob.Store
	signSecret []byte
}

// New loads (or creates) the persistent presign-signing secret and returns the
// service. The secret survives restarts so presigned URLs stay valid.
func New(ctx context.Context, eng data.Engine, store blob.Store) (*Service, error) {
	s := &Service{eng: eng, store: store}
	secret, err := s.loadOrCreateSecret(ctx)
	if err != nil {
		return nil, err
	}
	s.signSecret = secret
	return s, nil
}

func (s *Service) loadOrCreateSecret(ctx context.Context) ([]byte, error) {
	const key = "storage_sign_secret"
	row, err := s.eng.QueryRowCtx(ctx, `SELECT value FROM _ps_config WHERE key = ?`, key)
	if err == nil && row != nil {
		if v, ok := row["value"].(string); ok && v != "" {
			return hex.DecodeString(v)
		}
	}
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return nil, err
	}
	enc := hex.EncodeToString(buf)
	if _, err := s.eng.ExecCtx(ctx, `INSERT INTO _ps_config (key, value) VALUES (?, ?)`, key, enc); err != nil {
		// A concurrent boot may have inserted it first; re-read.
		row, rerr := s.eng.QueryRowCtx(ctx, `SELECT value FROM _ps_config WHERE key = ?`, key)
		if rerr == nil && row != nil {
			if v, ok := row["value"].(string); ok && v != "" {
				return hex.DecodeString(v)
			}
		}
		return nil, err
	}
	return buf, nil
}

// --- Buckets (admin) ------------------------------------------------------

// CreateBucket creates a bucket. Admin-only (enforced at the handler).
func (s *Service) CreateBucket(ctx context.Context, name string, public bool) (Bucket, error) {
	if !bucketNameRe.MatchString(name) {
		return Bucket{}, fmt.Errorf("%w: bucket name must be 2-63 chars, lowercase alphanumeric with . _ -", ErrInvalidName)
	}
	existing, _ := s.getBucket(ctx, name)
	if existing != nil {
		return Bucket{}, ErrBucketExists
	}
	now := nowISO()
	pub := 0
	if public {
		pub = 1
	}
	if _, err := s.eng.ExecCtx(ctx, `INSERT INTO _ps_buckets (name, public, created_at) VALUES (?, ?, ?)`, name, pub, now); err != nil {
		return Bucket{}, err
	}
	return Bucket{Name: name, Public: public, CreatedAt: now}, nil
}

// ListBuckets returns all buckets.
func (s *Service) ListBuckets(ctx context.Context) ([]Bucket, error) {
	rows, err := s.eng.QueryCtx(ctx, `SELECT name, public, created_at FROM _ps_buckets ORDER BY name`)
	if err != nil {
		return nil, err
	}
	out := make([]Bucket, 0, len(rows))
	for _, r := range rows {
		out = append(out, Bucket{Name: str(r["name"]), Public: asInt(r["public"]) == 1, CreatedAt: str(r["created_at"])})
	}
	return out, nil
}

// DeleteBucket removes a bucket and all of its objects (metadata + bytes).
func (s *Service) DeleteBucket(ctx context.Context, name string) error {
	if _, err := s.getBucket(ctx, name); err != nil {
		return err
	}
	rows, err := s.eng.QueryCtx(ctx, `SELECT id FROM _ps_objects WHERE bucket = ?`, name)
	if err != nil {
		return err
	}
	for _, r := range rows {
		_ = s.store.Delete(ctx, str(r["id"])) // best-effort: orphaned bytes are harmless
	}
	return s.eng.Transact(ctx, func(q data.Querier) error {
		if _, err := q.ExecCtx(ctx, `DELETE FROM _ps_objects WHERE bucket = ?`, name); err != nil {
			return err
		}
		_, err := q.ExecCtx(ctx, `DELETE FROM _ps_buckets WHERE name = ?`, name)
		return err
	})
}

func (s *Service) getBucket(ctx context.Context, name string) (*Bucket, error) {
	row, err := s.eng.QueryRowCtx(ctx, `SELECT name, public, created_at FROM _ps_buckets WHERE name = ?`, name)
	if errors.Is(err, data.ErrNoRows) || row == nil {
		return nil, ErrBucketNotFound
	}
	if err != nil {
		return nil, err
	}
	return &Bucket{Name: str(row["name"]), Public: asInt(row["public"]) == 1, CreatedAt: str(row["created_at"])}, nil
}

// --- Objects --------------------------------------------------------------

// Put stores (or overwrites) an object. Requires an authenticated principal or
// admin; the writer becomes the owner. Overwriting requires owner or admin.
func (s *Service) Put(ctx context.Context, p principal.Principal, bucket, key, contentType string, r io.Reader) (Object, error) {
	if _, err := s.getBucket(ctx, bucket); err != nil {
		return Object{}, err
	}
	if err := validateKey(key); err != nil {
		return Object{}, err
	}
	if !p.IsAdmin() && p.Subject == "" {
		return Object{}, ErrAnonWrite
	}
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	existing, _ := s.getObject(ctx, bucket, key)
	id := idgen.NewUUID()
	owner := p.Subject
	if existing != nil {
		if !p.IsAdmin() && existing.OwnerID != p.Subject {
			return Object{}, ErrDenied
		}
		id = existing.ID
		owner = existing.OwnerID // preserve original owner on overwrite
	}

	size, etag, err := s.store.Put(ctx, id, r)
	if err != nil {
		return Object{}, err
	}
	now := nowISO()
	if existing != nil {
		_, err = s.eng.ExecCtx(ctx,
			`UPDATE _ps_objects SET size=?, content_type=?, etag=?, updated_at=? WHERE id=?`,
			size, contentType, etag, now, id)
	} else {
		var ownerArg any
		if owner != "" {
			ownerArg = owner
		}
		_, err = s.eng.ExecCtx(ctx,
			`INSERT INTO _ps_objects (id, bucket, object_key, size, content_type, etag, owner_id, created_at, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			id, bucket, key, size, contentType, etag, ownerArg, now, now)
	}
	if err != nil {
		_ = s.store.Delete(ctx, id) // roll back bytes on metadata failure
		return Object{}, err
	}
	created := now
	if existing != nil {
		created = existing.CreatedAt
	}
	return Object{ID: id, Bucket: bucket, Key: key, Size: size, ContentType: contentType, ETag: etag, OwnerID: owner, CreatedAt: created, UpdatedAt: now}, nil
}

// Stat returns object metadata if the caller may read it.
func (s *Service) Stat(ctx context.Context, p principal.Principal, bucket, key string) (Object, error) {
	obj, err := s.authorizeRead(ctx, p, bucket, key)
	if err != nil {
		return Object{}, err
	}
	return *obj, nil
}

// Get returns object metadata and an open reader if the caller may read it. The
// caller must Close the reader.
func (s *Service) Get(ctx context.Context, p principal.Principal, bucket, key string) (Object, io.ReadCloser, error) {
	obj, err := s.authorizeRead(ctx, p, bucket, key)
	if err != nil {
		return Object{}, nil, err
	}
	rc, err := s.store.Get(ctx, obj.ID)
	if err != nil {
		if errors.Is(err, blob.ErrNotFound) {
			return Object{}, nil, ErrObjectNotFound
		}
		return Object{}, nil, err
	}
	return *obj, rc, nil
}

// GetSigned returns metadata and a reader for a presigned request (the signature
// is the capability; no principal check beyond a valid, unexpired signature).
func (s *Service) GetSigned(ctx context.Context, bucket, key string, expUnix int64, sig string) (Object, io.ReadCloser, error) {
	if !s.verify(bucket, key, expUnix, sig) {
		return Object{}, nil, ErrDenied
	}
	obj, err := s.getObject(ctx, bucket, key)
	if err != nil {
		return Object{}, nil, err
	}
	rc, err := s.store.Get(ctx, obj.ID)
	if err != nil {
		if errors.Is(err, blob.ErrNotFound) {
			return Object{}, nil, ErrObjectNotFound
		}
		return Object{}, nil, err
	}
	return *obj, rc, nil
}

func (s *Service) authorizeRead(ctx context.Context, p principal.Principal, bucket, key string) (*Object, error) {
	b, err := s.getBucket(ctx, bucket)
	if err != nil {
		return nil, err
	}
	obj, err := s.getObject(ctx, bucket, key)
	if err != nil {
		return nil, err
	}
	if p.IsAdmin() || b.Public {
		return obj, nil
	}
	if p.Subject != "" && obj.OwnerID == p.Subject {
		return obj, nil
	}
	return nil, ErrDenied
}

// Delete removes an object (owner or admin).
func (s *Service) Delete(ctx context.Context, p principal.Principal, bucket, key string) error {
	obj, err := s.getObject(ctx, bucket, key)
	if err != nil {
		return err
	}
	if !p.IsAdmin() && !(p.Subject != "" && obj.OwnerID == p.Subject) {
		return ErrDenied
	}
	if _, err := s.eng.ExecCtx(ctx, `DELETE FROM _ps_objects WHERE id = ?`, obj.ID); err != nil {
		return err
	}
	_ = s.store.Delete(ctx, obj.ID)
	return nil
}

// List returns objects in a bucket the caller may see: admin sees all; an
// authenticated caller sees only objects they own; anon sees nothing.
func (s *Service) List(ctx context.Context, p principal.Principal, bucket string) ([]Object, error) {
	if _, err := s.getBucket(ctx, bucket); err != nil {
		return nil, err
	}
	var (
		rows []data.Row
		err  error
	)
	switch {
	case p.IsAdmin():
		rows, err = s.eng.QueryCtx(ctx, objectCols+` WHERE bucket = ? ORDER BY object_key`, bucket)
	case p.Subject != "":
		rows, err = s.eng.QueryCtx(ctx, objectCols+` WHERE bucket = ? AND owner_id = ? ORDER BY object_key`, bucket, p.Subject)
	default:
		return []Object{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := make([]Object, 0, len(rows))
	for _, r := range rows {
		out = append(out, rowToObject(r))
	}
	return out, nil
}

// --- Presigning -----------------------------------------------------------

// Presign mints a time-limited signed path for GET access to an object. Only the
// owner or an admin may presign. ttl is clamped to [1s, 7d].
func (s *Service) Presign(ctx context.Context, p principal.Principal, bucket, key string, ttl time.Duration) (path string, expiresAt string, err error) {
	obj, err := s.getObject(ctx, bucket, key)
	if err != nil {
		return "", "", err
	}
	if !p.IsAdmin() && !(p.Subject != "" && obj.OwnerID == p.Subject) {
		return "", "", ErrDenied
	}
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}
	if ttl > maxObjectTTL {
		ttl = maxObjectTTL
	}
	exp := time.Now().Add(ttl).Unix()
	sig := s.sign(bucket, key, exp)
	path = fmt.Sprintf("/v1/storage/%s/%s?exp=%d&sig=%s", bucket, key, exp, sig)
	return path, time.Unix(exp, 0).UTC().Format(time.RFC3339), nil
}

func (s *Service) sign(bucket, key string, exp int64) string {
	mac := hmac.New(sha256.New, s.signSecret)
	fmt.Fprintf(mac, "%s\n%s\n%d", bucket, key, exp)
	return hex.EncodeToString(mac.Sum(nil))
}

func (s *Service) verify(bucket, key string, exp int64, sig string) bool {
	if time.Now().Unix() > exp {
		return false
	}
	want := s.sign(bucket, key, exp)
	return hmac.Equal([]byte(want), []byte(sig))
}

// --- helpers --------------------------------------------------------------

const objectCols = `SELECT id, bucket, object_key, size, content_type, etag, owner_id, created_at, updated_at FROM _ps_objects`

func (s *Service) getObject(ctx context.Context, bucket, key string) (*Object, error) {
	row, err := s.eng.QueryRowCtx(ctx, objectCols+` WHERE bucket = ? AND object_key = ?`, bucket, key)
	if errors.Is(err, data.ErrNoRows) || row == nil {
		return nil, ErrObjectNotFound
	}
	if err != nil {
		return nil, err
	}
	o := rowToObject(row)
	return &o, nil
}

func rowToObject(r data.Row) Object {
	return Object{
		ID:          str(r["id"]),
		Bucket:      str(r["bucket"]),
		Key:         str(r["object_key"]),
		Size:        asInt(r["size"]),
		ContentType: str(r["content_type"]),
		ETag:        str(r["etag"]),
		OwnerID:     str(r["owner_id"]),
		CreatedAt:   str(r["created_at"]),
		UpdatedAt:   str(r["updated_at"]),
	}
}

func validateKey(key string) error {
	if key == "" || len(key) > maxKeyLen {
		return fmt.Errorf("%w: key must be 1-%d characters", ErrInvalidKey, maxKeyLen)
	}
	if strings.Contains(key, "..") || strings.HasPrefix(key, "/") || strings.Contains(key, "//") {
		return fmt.Errorf("%w: key must not contain '..' or empty path segments", ErrInvalidKey)
	}
	for _, r := range key {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("%w: key must not contain control characters", ErrInvalidKey)
		}
	}
	return nil
}

// ParseTTL parses a presign TTL from seconds (query value), defaulting to 300s.
func ParseTTL(s string) time.Duration {
	if s == "" {
		return 5 * time.Minute
	}
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return 5 * time.Minute
	}
	return time.Duration(n) * time.Second
}

func nowISO() string { return time.Now().UTC().Format(time.RFC3339Nano) }

func str(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case []byte:
		return string(x)
	case nil:
		return ""
	default:
		return fmt.Sprintf("%v", x)
	}
}

func asInt(v any) int64 {
	switch n := v.(type) {
	case int64:
		return n
	case int:
		return int64(n)
	case float64:
		return int64(n)
	}
	return 0
}
