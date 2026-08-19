package pipeline

import (
	"sync"
	"time"
)

// Activity is the in-memory record of the Agent-backed work a process has in
// flight. Every other progress signal this system has is written when a unit of
// work finishes: an audited Agent call carries its own duration, and a job's
// state moves once the stage is done with it. A single call runs for minutes,
// so between those writes there is nothing at all to read — which is what makes
// a working process and a hung one look the same.
//
// It is deliberately not persisted. What is in flight is true only of a running
// process, and a restart ends every unit of work it describes.
type Activity struct {
	mu      sync.Mutex
	next    int64
	running map[int64]Unit
}

// Unit is one piece of work in flight.
type Unit struct {
	// Stage is the pipeline stage: fetch, filter, score, or letter.
	Stage string
	// JobID is the job being worked on; zero when the unit covers no single job.
	JobID     int64
	StartedAt time.Time
}

// Begin records a unit of work and returns the function that ends it. The end
// function is safe to call on a nil Activity and safe to call twice, so callers
// can defer it without guarding.
func (a *Activity) Begin(stage string, jobID int64, at time.Time) func() {
	if a == nil {
		return func() {}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.running == nil {
		a.running = map[int64]Unit{}
	}
	a.next++
	id := a.next
	a.running[id] = Unit{Stage: stage, JobID: jobID, StartedAt: at}
	return func() {
		a.mu.Lock()
		defer a.mu.Unlock()
		delete(a.running, id)
	}
}

// InFlight lists what is running now, oldest first, so a reader sees the unit
// that has been waiting longest at the top.
func (a *Activity) InFlight() []Unit {
	if a == nil {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	units := make([]Unit, 0, len(a.running))
	for _, unit := range a.running {
		units = append(units, unit)
	}
	sortUnits(units)
	return units
}

func sortUnits(units []Unit) {
	for i := 1; i < len(units); i++ {
		for j := i; j > 0 && units[j].StartedAt.Before(units[j-1].StartedAt); j-- {
			units[j], units[j-1] = units[j-1], units[j]
		}
	}
}
