package httpapi

import (
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/dibakshya01/purple-sparrow/internal/apierr"
	"github.com/dibakshya01/purple-sparrow/internal/principal"
	"github.com/dibakshya01/purple-sparrow/internal/storage"
)

func (s *Server) mountStorageRoutes(g chi.Router) {
	// Bucket management (admin). "buckets" is a reserved first segment, so it never
	// collides with a bucket name in the object routes below.
	g.Post("/v1/storage/buckets", s.handleCreateBucket)
	g.Get("/v1/storage/buckets", s.handleListBuckets)
	g.Delete("/v1/storage/buckets/{bucket}", s.handleDeleteBucket)

	// Objects. Key is the trailing wildcard and may contain slashes.
	g.Get("/v1/storage/{bucket}", s.handleListObjects)
	g.Put("/v1/storage/{bucket}/*", s.handlePutObject)
	g.Get("/v1/storage/{bucket}/*", s.handleGetObject)
	g.Delete("/v1/storage/{bucket}/*", s.handleDeleteObject)
}

func (s *Server) handleCreateBucket(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	var in struct {
		Name   string `json:"name"`
		Public bool   `json:"public"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	b, err := s.deps.Storage.CreateBucket(r.Context(), in.Name, in.Public)
	if err != nil {
		apierr.Write(w, r, mapDomainError(err))
		return
	}
	writeJSON(w, r, http.StatusCreated, b)
}

func (s *Server) handleListBuckets(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	bs, err := s.deps.Storage.ListBuckets(r.Context())
	if err != nil {
		apierr.Write(w, r, mapDomainError(err))
		return
	}
	writeJSON(w, r, http.StatusOK, map[string]any{"buckets": bs})
}

func (s *Server) handleDeleteBucket(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	if err := s.deps.Storage.DeleteBucket(r.Context(), chi.URLParam(r, "bucket")); err != nil {
		apierr.Write(w, r, mapDomainError(err))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleListObjects(w http.ResponseWriter, r *http.Request) {
	p := principal.FromContext(r.Context())
	objs, err := s.deps.Storage.List(r.Context(), p, chi.URLParam(r, "bucket"))
	if err != nil {
		apierr.Write(w, r, mapDomainError(err))
		return
	}
	writeJSON(w, r, http.StatusOK, map[string]any{"objects": objs})
}

func (s *Server) handlePutObject(w http.ResponseWriter, r *http.Request) {
	p := principal.FromContext(r.Context())
	bucket := chi.URLParam(r, "bucket")
	key := chi.URLParam(r, "*")

	// Cap object size; MaxBytesReader aborts the request and signals the client.
	body := http.MaxBytesReader(w, r.Body, s.cfg.StorageMaxObjectBytes)
	obj, err := s.deps.Storage.Put(r.Context(), p, bucket, key, r.Header.Get("Content-Type"), body)
	if err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			apierr.Write(w, r, apierr.New(http.StatusRequestEntityTooLarge, "object_too_large",
				"The object exceeds the maximum allowed size.",
				"Reduce the object size, or raise PS_STORAGE_MAX_OBJECT_BYTES on the server.",
				"/docs/storage"))
			return
		}
		apierr.Write(w, r, mapDomainError(err))
		return
	}
	writeJSON(w, r, http.StatusCreated, obj)
}

func (s *Server) handleGetObject(w http.ResponseWriter, r *http.Request) {
	bucket := chi.URLParam(r, "bucket")
	key := chi.URLParam(r, "*")
	q := r.URL.Query()

	// Presigned request: the signature is the capability, checked without a principal.
	if sig := q.Get("sig"); sig != "" {
		exp, _ := strconv.ParseInt(q.Get("exp"), 10, 64)
		obj, rc, err := s.deps.Storage.GetSigned(r.Context(), bucket, key, exp, sig)
		if err != nil {
			apierr.Write(w, r, mapDomainError(err))
			return
		}
		streamObject(w, obj, rc)
		return
	}

	p := principal.FromContext(r.Context())

	// Presign request: mint and return a signed URL instead of the bytes.
	if ttl := q.Get("presign"); ttl != "" {
		path, expires, err := s.deps.Storage.Presign(r.Context(), p, bucket, key, storage.ParseTTL(ttl))
		if err != nil {
			apierr.Write(w, r, mapDomainError(err))
			return
		}
		writeJSON(w, r, http.StatusOK, map[string]any{"url": path, "expires_at": expires})
		return
	}

	obj, rc, err := s.deps.Storage.Get(r.Context(), p, bucket, key)
	if err != nil {
		apierr.Write(w, r, mapDomainError(err))
		return
	}
	streamObject(w, obj, rc)
}

func (s *Server) handleDeleteObject(w http.ResponseWriter, r *http.Request) {
	p := principal.FromContext(r.Context())
	if err := s.deps.Storage.Delete(r.Context(), p, chi.URLParam(r, "bucket"), chi.URLParam(r, "*")); err != nil {
		apierr.Write(w, r, mapDomainError(err))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func streamObject(w http.ResponseWriter, obj storage.Object, rc io.ReadCloser) {
	defer rc.Close()
	w.Header().Set("Content-Type", obj.ContentType)
	w.Header().Set("Content-Length", strconv.FormatInt(obj.Size, 10))
	w.Header().Set("ETag", "\""+obj.ETag+"\"")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	// User-uploaded bytes are served from the API origin (which the dashboard also
	// uses and where an admin key lives in sessionStorage). An uploaded text/html or
	// SVG could otherwise run inline script on this origin. `sandbox` puts the
	// response in an opaque origin with scripts disabled, and `default-src 'none'`
	// blocks sub-resource loads — images/downloads still work, active content cannot.
	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, rc)
}
