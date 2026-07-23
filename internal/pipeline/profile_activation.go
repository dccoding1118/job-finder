package pipeline

import (
	"context"

	"github.com/dccoding1118/job-finder/internal/profile"
)

// ActivateProfile applies the new Profile rules without invoking an Agent.
func (p Pipeline) ActivateProfile(ctx context.Context, revision string, value profile.Profile) (profile.Activation, error) {
	filter := FilterFromProfile(value)
	stats, err := p.Store.ActivateProfile(ctx, revision, filter.Match)
	if err != nil {
		return profile.Activation{}, err
	}
	return profile.Activation{PartialScreened: stats.PartialScreened, Requeued: stats.Requeued, Protected: stats.Protected, Unchanged: stats.Unchanged}, nil
}
