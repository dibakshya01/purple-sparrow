// Package openapi embeds the OpenAPI 3.1 contract (ADR-0004) so the single binary
// serves it at GET /openapi.yaml. It is the source of truth for the REST surface,
// which is also exposed as MCP tools.
package openapi

import _ "embed"

//go:embed openapi.yaml
var Spec []byte
