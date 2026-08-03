package pipeline

import (
	"sync"
	"sync/atomic"
)

// Gate serializes the Agent-backed units of work inside one process and lets a
// user's single-job request cut ahead of the resident worker's batch.
//
// It guards the Agent call itself rather than a whole stage pass: a pass may
// consume dozens of jobs, so a request that had to wait for the pass would wait
// minutes. Waiting for the one call in flight is the shortest wait that still
// keeps the runner sequential. Batch loops additionally check Yield between
// jobs and stop early when someone is waiting; the jobs they leave behind keep
// their state and are picked up on the next pass.
//
// Claims cover the other half: the worker's pick and a priority request can
// name the same job, and without a claim both would pay for the same call.
// Correctness of the result still rests on the store's expected-state and
// expected-revision CAS — the gate only prevents the duplicate spend.
//
// A nil *Gate is a working gate with no serialization, which is what the CLI
// stages and the unit tests use.
type Gate struct {
	mu      sync.Mutex
	waiting atomic.Int64
	claims  sync.Map
}

// acquire takes the shared gate for one Agent call, as the user's request or as
// background work.
func (p Pipeline) acquire(priority bool) {
	if priority {
		p.Gate.AcquirePriority()
		return
	}
	p.Gate.Acquire()
}

// Acquire takes the gate for one background unit of work.
func (g *Gate) Acquire() {
	if g == nil {
		return
	}
	g.mu.Lock()
}

// AcquirePriority takes the gate for the user's own request, announcing the
// wait so a batch loop stops handing itself more work.
func (g *Gate) AcquirePriority() {
	if g == nil {
		return
	}
	g.waiting.Add(1)
	g.mu.Lock()
	g.waiting.Add(-1)
}

// Release hands the gate back.
func (g *Gate) Release() {
	if g == nil {
		return
	}
	g.mu.Unlock()
}

// Yield reports whether a priority request is waiting, which is a batch loop's
// signal to stop after the job it has finished.
func (g *Gate) Yield() bool {
	if g == nil {
		return false
	}
	return g.waiting.Load() > 0
}

// Claim marks one job as being worked on in this process. It reports false when
// the job is already claimed, which means the other path is paying for it.
func (g *Gate) Claim(jobID int64) bool {
	if g == nil {
		return true
	}
	_, loaded := g.claims.LoadOrStore(jobID, struct{}{})
	return !loaded
}

// Unclaim releases a claim taken by Claim.
func (g *Gate) Unclaim(jobID int64) {
	if g == nil {
		return
	}
	g.claims.Delete(jobID)
}
