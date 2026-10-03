# Storage (objects & buckets)

Purple Sparrow stores binary objects (images, uploads, exports) in **buckets**.
Object bytes live in a blob store (local filesystem on the solo tier, or an
S3-compatible service at scale); metadata and access control live in the backend.

## Authorization (deny-by-default, ownership-scoped)

This is distinct from the **record policy engine** (which governs table rows):

- Buckets are created and deleted by **project_admin**.
- A bucket may be **public** (its objects are world-readable) or private (default).
- **Uploading requires an authenticated identity.** The uploader becomes the
  object's *owner*. Anonymous uploads are rejected.
- **Reading** an object is allowed when: the bucket is public, OR you are the
  owner, OR you are admin, OR the request carries a valid **presigned signature**.
- **Overwriting** and **deleting** require the owner or admin.
- **Listing** a bucket returns only the objects you own (admin sees all).

## Endpoints

| Method & path | Who | Purpose |
|---|---|---|
| `POST /v1/storage/buckets` | admin | Create a bucket: `{"name":"assets","public":false}` |
| `GET /v1/storage/buckets` | admin | List buckets |
| `DELETE /v1/storage/buckets/{bucket}` | admin | Delete a bucket and all its objects |
| `PUT /v1/storage/{bucket}/{key}` | authenticated | Upload (raw body; set `Content-Type`) |
| `GET /v1/storage/{bucket}/{key}` | owner/public/admin | Download the bytes |
| `GET /v1/storage/{bucket}/{key}?presign=SECONDS` | owner/admin | Mint a time-limited signed URL |
| `GET /v1/storage/{bucket}/{key}?exp=…&sig=…` | anyone with the link | Download via a presigned URL |
| `GET /v1/storage/{bucket}` | owner/admin | List objects |
| `DELETE /v1/storage/{bucket}/{key}` | owner/admin | Delete an object |

`{key}` may contain slashes (`photos/2026/cover.jpg`). The `etag` returned on
upload is the sha256 of the bytes. Objects are size-capped
(`PS_STORAGE_MAX_OBJECT_BYTES`, default 100 MiB); exceeding it returns
`413 object_too_large`.

## Example

```
# admin creates a private bucket
POST /v1/storage/buckets        {"name":"user-uploads"}

# a signed-in user uploads (becomes the owner)
PUT  /v1/storage/user-uploads/avatar.png      <bytes>   → 201 {id,size,etag,...}

# the owner shares a 5-minute link without exposing their credential
GET  /v1/storage/user-uploads/avatar.png?presign=300    → 200 {url, expires_at}

# anyone with that url fetches the bytes until it expires
GET  /v1/storage/user-uploads/avatar.png?exp=…&sig=…     → 200 <bytes>
```
