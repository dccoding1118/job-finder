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
		Area     []string
		MaxPages int
	}
	RawJob struct {
		Source, ExternalID, URL, Title, CompanyName, CompanyInfo, Description, Location, RemoteType string
		SalaryMin, SalaryMax                                                                        *int
	}
)

func (j RawJob) Partial() bool { return strings.TrimSpace(j.Description) == "" }

type Source interface {
	Name() string
	Fetch(context.Context, SearchSpec) ([]RawJob, error)
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
	for _, v := range s.Area {
		if strings.TrimSpace(v) == "" {
			return fmt.Errorf("crawler: search terms must not be empty")
		}
	}
	return nil
}
