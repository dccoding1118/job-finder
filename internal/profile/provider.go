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
	"strconv"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

const revisionSchema = "jobfinder-profile-v1\n"

var (
	ErrNotReady = errors.New("profile is not ready")
	ErrConflict = errors.New("profile file changed")
)

type Issue struct {
	Code    string `json:"code"`
	Path    string `json:"path"`
	Message string `json:"message"`
}

type Snapshot struct {
	Status   string
	Profile  *Profile
	YAML     string
	Revision string
	ETag     string
	Issues   []Issue
}

type Activation struct {
	PartialScreened int `json:"partial_screened"`
	Requeued        int `json:"requeued"`
	Protected       int `json:"protected"`
	Unchanged       int `json:"unchanged"`
}

type ActivationFunc func(context.Context, string, Profile) (Activation, error)

type SaveResult struct {
	Snapshot        Snapshot
	SemanticChanged bool
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
	if issues := ValidateForSave(value, p.denylist); len(issues) > 0 {
		return SaveResult{}, ValidationError{Issues: issues}
	}
	canonicalYAML, err := yaml.Marshal(value)
	if err != nil {
		return SaveResult{}, fmt.Errorf("encode profile YAML: %w", err)
	}
	revision, err := Revision(value)
	if err != nil {
		return SaveResult{}, err
	}
	semanticChanged := p.current.Status != "ready" || p.current.Revision != revision
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
	p.current = Snapshot{Status: "ready", Profile: &profileCopy, YAML: string(canonicalYAML), Revision: revision, ETag: newETag, Issues: []Issue{}}
	select {
	case p.changed <- struct{}{}:
	default:
	}
	return SaveResult{Snapshot: cloneSnapshot(p.current), SemanticChanged: semanticChanged}, nil
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
	return value, nil
}

func Revision(value Profile) (string, error) {
	canonical, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("encode canonical profile: %w", err)
	}
	digest := sha256.Sum256(append([]byte(revisionSchema), canonical...))
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
	revision, err := Revision(value)
	if err != nil {
		return Snapshot{}, err
	}
	copy := cloneProfile(value)
	return Snapshot{Status: "ready", Profile: &copy, YAML: string(canonicalYAML), Revision: revision, ETag: ETag(contents), Issues: []Issue{}}, nil
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
	dir, err := os.Open(directory) // #nosec G304 -- configured local Profile directory.
	if err == nil {
		err = dir.Sync()
		_ = dir.Close()
	}
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
