package profile

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

const (
	filterRevisionSchema = "jobfinder-profile-filter-v5\n"
	scoreRevisionSchema  = "jobfinder-profile-score-v5\n"
)

var (
	ErrNotReady = errors.New("profile is not ready")
	ErrConflict = errors.New("profile file changed")
)

type Issue struct {
	Code    string `json:"code"`
	Path    string `json:"path"`
	Message string `json:"message"`
}

// Snapshot carries both revisions because the two gates rerun independently:
// a change to the soft rules must not send every job back through screening.
type Snapshot struct {
	Status         string
	Profile        *Profile
	YAML           string
	FilterRevision string
	ScoreRevision  string
	ETag           string
	Issues         []Issue
}

type Activation struct {
	PartialScreened int `json:"partial_screened"`
	Refiltered      int `json:"refiltered"`
	Requeued        int `json:"requeued"`
	Protected       int `json:"protected"`
	Unchanged       int `json:"unchanged"`
}

// Revisions names one activation's target pair.
type Revisions struct {
	Filter string
	Score  string
}

type ActivationFunc func(context.Context, Revisions, Profile) (Activation, error)

type SaveResult struct {
	Snapshot      Snapshot
	FilterChanged bool
	ScoreChanged  bool
}

// Provider owns the process-wide immutable Profile snapshot and its disk file.
type Provider struct {
	mu       sync.RWMutex
	path     string
	denylist []string
	current  Snapshot
	changed  chan struct{}
}

func NewProvider(path string, denylist []string) (*Provider, error) {
	p := &Provider{path: path, denylist: append([]string(nil), denylist...), changed: make(chan struct{}, 1)}
	snapshot, err := readSnapshot(path, denylist)
	if err != nil {
		return nil, err
	}
	p.current = snapshot
	return p, nil
}

func (p *Provider) Changes() <-chan struct{} { return p.changed }

func (p *Provider) Current() Snapshot {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return cloneSnapshot(p.current)
}

func (p *Provider) Ready() (Snapshot, error) {
	snapshot := p.Current()
	if snapshot.Status != "ready" || snapshot.Profile == nil {
		return Snapshot{}, ErrNotReady
	}
	return snapshot, nil
}

// Save validates and atomically replaces the YAML before publishing the new
// runtime snapshot. Existing jobs keep their evaluation revision until the
// user explicitly requests reprocessing.
func (p *Provider) Save(expectedETag string, value Profile) (SaveResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	contents, exists, err := readFile(p.path)
	if err != nil {
		return SaveResult{}, err
	}
	actualETag := `"missing"`
	if exists {
		actualETag = ETag(contents)
	}
	if expectedETag == "" || expectedETag != actualETag {
		return SaveResult{}, ErrConflict
	}
	// The totals are materialized here rather than accepted from the caller, so a
	// stored Profile can never state a year count its experiences disagree with.
	value.normalize()
	value.materializeDerived()
	if issues := ValidateForSave(value, p.denylist); len(issues) > 0 {
		return SaveResult{}, ValidationError{Issues: issues}
	}
	canonicalYAML, err := yaml.Marshal(value)
	if err != nil {
		return SaveResult{}, fmt.Errorf("encode profile YAML: %w", err)
	}
	filterRevision, scoreRevision, err := RevisionPair(value)
	if err != nil {
		return SaveResult{}, err
	}
	fresh := p.current.Status != "ready"
	filterChanged := fresh || p.current.FilterRevision != filterRevision
	scoreChanged := fresh || p.current.ScoreRevision != scoreRevision
	newETag := ETag(canonicalYAML)
	if bytes.Equal(contents, canonicalYAML) && exists {
		newETag = actualETag
	}
	if !bytes.Equal(contents, canonicalYAML) || !exists {
		if writeErr := atomicWrite(p.path, canonicalYAML); writeErr != nil {
			return SaveResult{}, fmt.Errorf("save profile: %w", writeErr)
		}
	}
	profileCopy := cloneProfile(value)
	p.current = Snapshot{Status: "ready", Profile: &profileCopy, YAML: string(canonicalYAML), FilterRevision: filterRevision, ScoreRevision: scoreRevision, ETag: newETag, Issues: []Issue{}}
	select {
	case p.changed <- struct{}{}:
	default:
	}
	return SaveResult{Snapshot: cloneSnapshot(p.current), FilterChanged: filterChanged, ScoreChanged: scoreChanged}, nil
}

type ValidationError struct{ Issues []Issue }

func (e ValidationError) Error() string { return "profile validation failed" }

func DecodeJSON(reader io.Reader) (Profile, error) {
	var value Profile
	decoder := json.NewDecoder(reader)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return Profile{}, err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return Profile{}, errors.New("profile: request must contain one JSON object")
	}
	// `derived` is materialized from the experience list; accepting it as input
	// would let a caller state totals that contradict what they were derived from.
	if !value.Derived.Empty() {
		return Profile{}, errors.New("profile: derived is computed by the system and must not be supplied")
	}
	return value, nil
}

// filterRevisionInput is the exact field set the screening gate reads. A
// revision hashes its own gate's fields only, so changing the soft rules cannot
// send jobs back through screening.
type filterRevisionInput struct {
	Requirements   Requirements           `json:"requirements"`
	Qualifications Qualifications         `json:"qualifications"`
	Experiences    []filterExperienceView `json:"experiences"`
}

type filterExperienceView struct {
	Industry          string   `json:"industry"`
	Years             float64  `json:"years"`
	IsManagement      bool     `json:"is_management"`
	ExcludeFromTotals bool     `json:"exclude_from_totals"`
	Skills            []string `json:"skills"`
}

// scoreRevisionInput is the field set the scoring gate reads. `remote`,
// `locations` and the three qualification lists appear in both inputs because
// both gates genuinely read them: the shared cost of a field serving two gates.
type scoreRevisionInput struct {
	Intents        Intents         `json:"intents"`
	Remote         string          `json:"remote"`
	Locations      []string        `json:"locations"`
	Skills         []SkillEntry    `json:"skills"`
	Certifications []Certification `json:"certifications"`
	Languages      []LanguageEntry `json:"languages"`
}

// RevisionPair returns the filter and score revisions of one Profile.
func RevisionPair(value Profile) (string, string, error) {
	filter, err := FilterRevision(value)
	if err != nil {
		return "", "", err
	}
	score, err := ScoreRevision(value)
	if err != nil {
		return "", "", err
	}
	return filter, score, nil
}

func FilterRevision(value Profile) (string, error) {
	input := filterRevisionInput{Requirements: value.Requirements, Qualifications: value.Qualifications}
	for _, experience := range value.Experiences {
		input.Experiences = append(input.Experiences, filterExperienceView{
			Industry: experience.Industry, Years: experience.Years, IsManagement: experience.IsManagement,
			ExcludeFromTotals: experience.ExcludeFromTotals, Skills: experience.Skills,
		})
	}
	return digestRevision(filterRevisionSchema, input)
}

func ScoreRevision(value Profile) (string, error) {
	return digestRevision(scoreRevisionSchema, scoreRevisionInput{
		Intents: value.Intents, Remote: value.Requirements.Remote, Locations: value.Requirements.Locations,
		Skills: value.Qualifications.Skills, Certifications: value.Qualifications.Certifications,
		Languages: value.Qualifications.Languages,
	})
}

func digestRevision(schema string, input any) (string, error) {
	canonical, err := json.Marshal(input)
	if err != nil {
		return "", fmt.Errorf("encode canonical profile: %w", err)
	}
	digest := sha256.Sum256(append([]byte(schema), canonical...))
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

func ETag(contents []byte) string {
	digest := sha256.Sum256(contents)
	return `"sha256:` + hex.EncodeToString(digest[:]) + `"`
}

func ValidateForSave(value Profile, denylist []string) []Issue {
	if err := value.Validate(); err != nil {
		return []Issue{{Code: "schema_invalid", Message: err.Error()}}
	}
	issues := make([]Issue, 0)
	walkStrings(reflect.ValueOf(value), "", func(path, text string) {
		for _, finding := range FindPII(text, denylist) {
			issues = append(issues, Issue{Code: "pii_detected", Path: path, Message: "This field contains personal information (" + finding.Kind + ")"})
		}
	})
	return issues
}

func readSnapshot(path string, denylist []string) (Snapshot, error) {
	contents, exists, err := readFile(path)
	if err != nil {
		return Snapshot{}, err
	}
	if !exists {
		return Snapshot{Status: "missing", ETag: `"missing"`, Issues: []Issue{}}, nil
	}
	value, err := DecodeYAML(contents)
	if err != nil {
		return Snapshot{Status: "invalid", ETag: ETag(contents), Issues: []Issue{{Code: "schema_invalid", Message: "Profile YAML is invalid"}}}, nil
	}
	if issues := ValidateForSave(value, denylist); len(issues) > 0 {
		return Snapshot{Status: "invalid", ETag: ETag(contents), Issues: issues}, nil
	}
	canonicalYAML, err := yaml.Marshal(value)
	if err != nil {
		return Snapshot{}, err
	}
	filterRevision, scoreRevision, err := RevisionPair(value)
	if err != nil {
		return Snapshot{}, err
	}
	copy := cloneProfile(value)
	return Snapshot{Status: "ready", Profile: &copy, YAML: string(canonicalYAML), FilterRevision: filterRevision, ScoreRevision: scoreRevision, ETag: ETag(contents), Issues: []Issue{}}, nil
}

func readFile(path string) ([]byte, bool, error) {
	contents, err := os.ReadFile(path) // #nosec G304 -- configured local Profile path.
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("read profile: %w", err)
	}
	return contents, true, nil
}

func atomicWrite(path string, contents []byte) error {
	directory := filepath.Dir(path)
	temporary, err := os.CreateTemp(directory, ".profile-*.tmp") // #nosec G304 -- configured local Profile directory.
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer func() { _ = os.Remove(temporaryPath) }()
	if chmodErr := temporary.Chmod(0o600); chmodErr != nil {
		_ = temporary.Close()
		return chmodErr
	}
	if _, writeErr := temporary.Write(contents); writeErr != nil {
		_ = temporary.Close()
		return writeErr
	}
	if syncErr := temporary.Sync(); syncErr != nil {
		_ = temporary.Close()
		return syncErr
	}
	if closeErr := temporary.Close(); closeErr != nil {
		return closeErr
	}
	if renameErr := os.Rename(temporaryPath, path); renameErr != nil {
		return renameErr
	}
	if chmodErr := os.Chmod(path, 0o600); chmodErr != nil {
		return chmodErr
	}
	return syncDirectory(directory)
}

// syncDirectory flushes the rename above so a crash cannot leave the Profile
// missing entirely. Windows has no directory fsync — opening a directory handle
// for sync fails outright — and NTFS commits the metadata of a replacing rename
// on its own, so the step is skipped there rather than turned into a save that
// always fails.
func syncDirectory(directory string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	dir, err := os.Open(directory) // #nosec G304 -- configured local Profile directory.
	if err != nil {
		return err
	}
	err = dir.Sync()
	_ = dir.Close()
	return err
}

func cloneSnapshot(value Snapshot) Snapshot {
	copy := value
	copy.Issues = append([]Issue(nil), value.Issues...)
	if value.Profile != nil {
		profileCopy := cloneProfile(*value.Profile)
		copy.Profile = &profileCopy
	}
	return copy
}

func cloneProfile(value Profile) Profile {
	encoded, _ := json.Marshal(value)
	var copy Profile
	_ = json.Unmarshal(encoded, &copy)
	return copy
}

func walkStrings(value reflect.Value, path string, visit func(string, string)) {
	switch value.Kind() {
	case reflect.Struct:
		typeOf := value.Type()
		for index := 0; index < value.NumField(); index++ {
			name := strings.Split(typeOf.Field(index).Tag.Get("json"), ",")[0]
			if name == "" || name == "-" {
				continue
			}
			childPath := name
			if path != "" {
				childPath = path + "." + name
			}
			walkStrings(value.Field(index), childPath, visit)
		}
	case reflect.Slice:
		for index := 0; index < value.Len(); index++ {
			walkStrings(value.Index(index), path+"["+strconv.Itoa(index)+"]", visit)
		}
	case reflect.String:
		visit(path, value.String())
	}
}
