package api

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/dccoding1118/job-finder/internal/store"
)

// The duplicate routes are the user's side of cross-source grouping: the program
// rules merge only what they are sure of, and everything else is decided here.
// All of them are synchronous store transactions — no background work, no Agent.

func (s *Server) duplicates(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method is not allowed")
		return
	}
	if _, pageErr := parsePagination(r); pageErr != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", pageErr.Error())
		return
	}
	candidates, err := s.store.ListDuplicateCandidates(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "unable to list duplicate candidates")
		return
	}
	selected, next, err := pageSlice(candidates, r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": selected, "next_cursor": next})
}

func (s *Server) duplicate(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/v1/duplicates/"), "/")
	id, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid_request", "candidate id must be a positive integer")
		return
	}
	if len(parts) != 2 || r.Method != http.MethodPost {
		writeError(w, http.StatusNotFound, "not_found", "route was not found")
		return
	}
	switch parts[1] {
	case "merge":
		s.mergeDuplicate(w, r, id)
	case "ignore":
		s.ignoreDuplicate(w, r, id)
	default:
		writeError(w, http.StatusNotFound, "not_found", "route was not found")
	}
}

// mergeDuplicate records that the user judged a suspected pair to be one job. The
// canonical copy of the merged group is returned, because that is the one they
// act on from here.
func (s *Server) mergeDuplicate(w http.ResponseWriter, r *http.Request, id int64) {
	canonicalID, err := s.store.MergeCandidate(r.Context(), id, s.cfg.Dedupe)
	if err != nil {
		s.candidateError(w, err, "unable to merge the duplicate candidate")
		return
	}
	detail, found, err := s.store.GetJobDetail(r.Context(), canonicalID)
	if err != nil || !found {
		writeError(w, http.StatusInternalServerError, "internal", "unable to read the merged job")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "merged", "job": s.jobViewWithGroup(r, detail)})
}

func (s *Server) ignoreDuplicate(w http.ResponseWriter, r *http.Request, id int64) {
	if err := s.store.IgnoreCandidate(r.Context(), id); err != nil {
		s.candidateError(w, err, "unable to ignore the duplicate candidate")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ignored"})
}

// unmergeJob restores one alias to the state and the standalone group it had
// before a merge, which is what makes an automatic merge safe to be wrong about.
func (s *Server) unmergeJob(w http.ResponseWriter, r *http.Request, id int64) {
	switch err := s.store.UnmergeJob(r.Context(), id); {
	case errors.Is(err, store.ErrNotMerged):
		writeError(w, http.StatusConflict, "not_merged", "only a merged job can be unmerged")
		return
	case errors.Is(err, sql.ErrNoRows):
		writeError(w, http.StatusNotFound, "not_found", "job was not found")
		return
	case err != nil:
		writeError(w, http.StatusInternalServerError, "internal", "unable to unmerge the job")
		return
	}
	detail, found, err := s.store.GetJobDetail(r.Context(), id)
	if err != nil || !found {
		writeError(w, http.StatusInternalServerError, "internal", "unable to read the unmerged job")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "unmerged", "job": s.jobViewWithGroup(r, detail)})
}

func (s *Server) candidateError(w http.ResponseWriter, err error, message string) {
	switch {
	case errors.Is(err, sql.ErrNoRows):
		writeError(w, http.StatusNotFound, "not_found", "duplicate candidate was not found")
	case errors.Is(err, store.ErrCandidateDecided):
		writeError(w, http.StatusConflict, "candidate_decided", "duplicate candidate was already decided")
	default:
		writeError(w, http.StatusInternalServerError, "internal", message)
	}
}
