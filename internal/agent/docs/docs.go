// Package docs serves Orange Crow's own documentation over the API — the
// `fetch-docs` surface an agent calls to learn the system. Content is embedded in
// the binary, so releases are self-contained and there is no user-input surface.
package docs

import (
	"embed"
	"errors"
	"io/fs"
	"sort"
	"strings"
)

//go:embed content/*.md
var content embed.FS

// ErrNotFound is returned for an unknown doc slug.
var ErrNotFound = errors.New("doc not found")

// Service serves embedded docs.
type Service struct {
	slugs []string
}

// New builds the docs Service, indexing available slugs at construction.
func New() *Service {
	entries, _ := fs.ReadDir(content, "content")
	var slugs []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".md") {
			slugs = append(slugs, strings.TrimSuffix(e.Name(), ".md"))
		}
	}
	sort.Strings(slugs)
	return &Service{slugs: slugs}
}

// Slugs returns the available doc slugs.
func (s *Service) Slugs() []string { return s.slugs }

// Get returns the markdown content for a slug, or ErrNotFound.
func (s *Service) Get(slug string) (string, error) {
	// slug is matched against the known set; never used to build an arbitrary path.
	for _, known := range s.slugs {
		if known == slug {
			b, err := content.ReadFile("content/" + slug + ".md")
			if err != nil {
				return "", err
			}
			return string(b), nil
		}
	}
	return "", ErrNotFound
}
