// Package crawler defines compliant public job sources.
package crawler

import (
	"context"
	"fmt"
	"strings"
)

type (
	SearchQuery struct {
		Direction string
		Keywords  []string
	}
	SearchSpec struct {
		Queries  []SearchQuery
		MaxPages int
	}
	RawJob struct {
		Source, ExternalID, URL, Title, CompanyName, CompanyInfo, Description, Location, RemoteType string
		SalaryMin, SalaryMax                                                                        *int
	}
)

func (j RawJob) Partial() bool { return strings.TrimSpace(j.Description) == "" }

// Batch is one unit of harvested progress. A source hands batches over as it
// goes rather than returning everything at the end: a fetch across several
// queries runs for many minutes, and a caller that only learns the outcome once
// stores nothing until then and cannot tell a slow source from a dead one. A
// batch with no jobs is still progress — it says the page was reached.
type Batch struct {
	// Direction names the search query this batch came from, and Page the list
	// page within it, so a reader can say how far along a fetch is.
	Direction string
	Page      int
	Jobs      []RawJob
}

type Source interface {
	Name() string
	// Fetch calls emit for every batch it harvests and returns when the whole
	// spec is covered. An error from emit stops the fetch and is returned as is,
	// so a caller that cannot store a batch does not pay for the rest of them.
	Fetch(ctx context.Context, spec SearchSpec, emit func(Batch) error) error
}

func (s SearchSpec) Validate() error {
	if len(s.Queries) == 0 || len(s.Queries) > 3 || s.MaxPages <= 0 {
		return fmt.Errorf("crawler: one to three queries and max pages are required")
	}
	for _, query := range s.Queries {
		if strings.TrimSpace(query.Direction) == "" || len(query.Keywords) == 0 {
			return fmt.Errorf("crawler: query direction and keywords are required")
		}
		for _, keyword := range query.Keywords {
			if strings.TrimSpace(keyword) == "" {
				return fmt.Errorf("crawler: search terms must not be empty")
			}
		}
	}
	return nil
}
