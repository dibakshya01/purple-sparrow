package httpapi

import (
	"errors"
	"net/http"

	"github.com/dibakshya01/purple-sparrow/internal/apierr"
	"github.com/dibakshya01/purple-sparrow/internal/catalog"
	"github.com/dibakshya01/purple-sparrow/internal/policy"
	"github.com/dibakshya01/purple-sparrow/internal/records"
)

// mapDomainError translates a service-layer error into the agent error envelope.
// 4xx client errors carry the descriptive message (safe); anything unrecognized
// becomes a generic 500 whose internal cause is logged, never sent.
func mapDomainError(err error) *apierr.Error {
	switch {
	case errors.Is(err, catalog.ErrTableExists):
		return apierr.New(http.StatusConflict, "table_exists", err.Error(),
			"A table with this name already exists. Use GET /meta to inspect it, or choose another name.",
			"/docs/errors#table_exists")
	case errors.Is(err, catalog.ErrTableNotFound):
		return apierr.New(http.StatusNotFound, "table_not_found", err.Error(),
			"Create the table with POST /v1/tables, or list tables with GET /meta.",
			"/docs/errors#table_not_found", "GET /meta")
	case errors.Is(err, catalog.ErrInvalidIdentifier), errors.Is(err, catalog.ErrReservedColumn):
		return apierr.New(http.StatusBadRequest, "invalid_identifier", err.Error(),
			"Use lowercase letters, digits and underscores; start with a letter or underscore; avoid the reserved _ps_ prefix and the managed columns id/created_at.",
			"/docs/errors#invalid_identifier")
	case errors.Is(err, catalog.ErrInvalidType):
		return apierr.New(http.StatusBadRequest, "invalid_column_type", err.Error(),
			"Use one of: text, integer, real, boolean, timestamp, uuid, json.",
			"/docs/errors#invalid_column_type")
	case errors.Is(err, catalog.ErrNoColumns):
		return apierr.New(http.StatusBadRequest, "validation_failed", err.Error(),
			"Provide at least one user column in `columns`.",
			"/docs/errors#validation_failed")

	case errors.Is(err, records.ErrPolicyDenied):
		return apierr.New(http.StatusForbidden, "policy_denied",
			"No policy grants this action to your role.",
			"Access is deny-by-default. Add a policy (POST /v1/policies) that grants your role this action, or authenticate as project_admin.",
			"/docs/policies", "GET /meta", "POST /v1/policies")
	case errors.Is(err, records.ErrRecordNotFound):
		return apierr.New(http.StatusNotFound, "record_not_found",
			"No record matched (it may not exist or a policy hides it).",
			"Verify the id, and that a select/update policy grants your role visibility of the row.",
			"/docs/errors#record_not_found")
	case errors.Is(err, records.ErrInvalidFilter):
		return apierr.New(http.StatusBadRequest, "invalid_filter", err.Error(),
			"Filters are ?column=op.value with op in eq,ne,lt,lte,gt,gte,like,in. Check the column exists via GET /meta.",
			"/docs/errors#invalid_filter")
	case errors.Is(err, records.ErrUnknownColumn), errors.Is(err, records.ErrReadOnlyColumn), errors.Is(err, records.ErrNoValues):
		return apierr.New(http.StatusBadRequest, "validation_failed", err.Error(),
			"Only existing, user-writable columns may be set. id and created_at are managed by the system. GET /meta lists columns.",
			"/docs/errors#validation_failed", "GET /meta")

	case errors.Is(err, policy.ErrInvalidExpr):
		return apierr.New(http.StatusBadRequest, "policy_invalid_expr", err.Error(),
			"A policy expression may reference the table's columns, auth.uid(), auth.role(), literals, and the operators = <> < <= > >= and/or/not/in. Check column names via GET /meta.",
			"/docs/policies#expressions")
	case errors.Is(err, policy.ErrInvalidAction), errors.Is(err, policy.ErrInvalidRole),
		errors.Is(err, policy.ErrNoRoles), errors.Is(err, policy.ErrMissingExpr):
		return apierr.New(http.StatusBadRequest, "policy_invalid", err.Error(),
			"action ∈ {select,insert,update,delete}; roles ⊆ {anon,authenticated,project_admin}; select/delete need `using`, insert needs `check`, update needs `using`.",
			"/docs/policies")

	default:
		return apierr.Internal("").WithInternal(err)
	}
}
