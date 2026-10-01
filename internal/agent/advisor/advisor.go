// Package advisor is an agent-facing linter: it inspects the catalog and policies
// and reports configuration issues an agent (or human) should act on, each with a
// remediation — the same self-correction affordance as the error envelope.
package advisor

import (
	"context"

	"github.com/dibakshya01/purple-sparrow/internal/catalog"
	"github.com/dibakshya01/purple-sparrow/internal/policy"
)

// Severity levels.
const (
	SevWarn = "warn"
	SevInfo = "info"
)

// Finding is one advisory result.
type Finding struct {
	Code        string `json:"code"`
	Severity    string `json:"severity"`
	Table       string `json:"table,omitempty"`
	Message     string `json:"message"`
	Remediation string `json:"remediation"`
}

// Service runs advisory checks.
type Service struct {
	cat *catalog.Service
	pol *policy.Service
}

// New returns an advisor Service.
func New(cat *catalog.Service, pol *policy.Service) *Service {
	return &Service{cat: cat, pol: pol}
}

// Analyze returns all findings across all tables.
func (s *Service) Analyze(ctx context.Context) ([]Finding, error) {
	tables, err := s.cat.ListTables(ctx)
	if err != nil {
		return nil, err
	}
	findings := []Finding{}
	for _, t := range tables {
		pols, err := s.pol.List(ctx, t)
		if err != nil {
			return nil, err
		}
		findings = append(findings, s.checkTable(t, pols)...)
	}
	return findings, nil
}

func (s *Service) checkTable(table string, pols []policy.Policy) []Finding {
	var out []Finding
	byAction := map[policy.Action]int{}
	for _, p := range pols {
		byAction[p.Action]++
		if p.Action == policy.ActionSelect && p.Using == "true" {
			out = append(out, Finding{
				Code: "table_select_all", Severity: SevInfo, Table: table,
				Message:     "A select policy grants unconditional read (using \"true\").",
				Remediation: "Confirm this table is meant to be world-readable to the listed roles; otherwise scope the policy (e.g. auth.uid() = owner_id).",
			})
		}
	}

	if len(pols) == 0 {
		out = append(out, Finding{
			Code: "table_no_policies", Severity: SevWarn, Table: table,
			Message:     "Table has no policies, so only project_admin can access it.",
			Remediation: "Add policies (POST /v1/policies) to grant non-admin roles access, or confirm this table is admin-only by design.",
		})
		return out
	}
	if byAction[policy.ActionSelect] == 0 {
		out = append(out, Finding{
			Code: "table_no_select_policy", Severity: SevInfo, Table: table,
			Message:     "Table has write policies but no select policy; non-admin clients can write rows they cannot read back.",
			Remediation: "Add a select policy if clients should read their data, or confirm write-only is intended.",
		})
	}
	return out
}
