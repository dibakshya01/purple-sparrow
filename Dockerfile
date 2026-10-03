# Purple Sparrow — single static binary in a minimal, non-root image.
# Build:  docker build -t purple-sparrow .
# Run:    docker run -p 8787:8787 -v ps-data:/data \
#           -e PS_ADMIN_API_KEY=ps_sk_... purple-sparrow

# --- build stage ---
FROM golang:1.27 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# CGO off → fully static binary (pure-Go SQLite via modernc). -trimpath for
# reproducibility; version info is injected at release time by goreleaser, so the
# plain image reports "dev".
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /out/purplesparrow ./cmd/purplesparrow

# --- runtime stage ---
# distroless/static: no shell, no package manager, includes CA certs (needed for
# S3/HTTPS) and a non-root user (uid 65532).
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/purplesparrow /usr/local/bin/purplesparrow

# Data dir for the embedded SQLite DB, signing keys, and local blobs. Mount a
# volume here in production; it must be writable by uid 65532.
ENV PS_DATA_DIR=/data \
    PS_ADDR=0.0.0.0:8787 \
    PS_LOG_FORMAT=json
WORKDIR /data
EXPOSE 8787
USER 65532:65532
ENTRYPOINT ["/usr/local/bin/purplesparrow"]
CMD ["serve"]
