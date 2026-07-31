package pipeline

import (
	"context"

	"github.com/dccoding1118/job-finder/internal/profile"
	"github.com/dccoding1118/job-finder/internal/store"
)

// ActivateProfile applies the new Profile rules without invoking an Agent. How
// far a job is sent back depends on which revision changed: a screening change
// re-screens everything, a scoring change only re-scores.
func (p Pipeline) ActivateProfile(ctx context.Context, revisions profile.Revisions, value profile.Profile) (profile.Activation, error) {
	filter := FilterFromProfile(value)
	stats, err := p.Store.ActivateProfile(ctx, store.Revisions{Filter: revisions.Filter, Score: revisions.Score}, filter.Match)
	if err != nil {
		return profile.Activation{}, err
	}
	return profile.Activation{
		PartialScreened: stats.PartialScreened, Refiltered: stats.Refiltered, Requeued: stats.Requeued,
		Protected: stats.Protected, Unchanged: stats.Unchanged,
	}, nil
}
