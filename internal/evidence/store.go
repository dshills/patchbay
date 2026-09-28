package evidence

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"patchbay/internal/fault"
	"patchbay/internal/identity"
	"patchbay/internal/jsonstrict"
	"patchbay/internal/localfs"
	"patchbay/pkg/protocol"
)

type Limits struct {
	MaxRuns          int
	MaxBytes         int64
	MaxRunBytes      int64
	MaxArtifactBytes int64
	MaxReceipts      int
}

func DefaultLimits() Limits { return Limits{1000, 1 << 30, 16 << 20, 4 << 20, 10000} }

type Options struct {
	Now func() time.Time
	// Fault is an injected filesystem failure boundary, used by deterministic tests.
	Fault func(operation, name string) error
}

type receipt struct {
	Digest  string    `json:"digest"`
	RunID   string    `json:"run_id"`
	JobID   string    `json:"job_id,omitempty"`
	Expires time.Time `json:"expires"`
}

type metadata struct {
	Version   int                          `json:"version"`
	HighWater time.Time                    `json:"high_water"`
	Deleted   map[string]receipt           `json:"deleted"`
	Baselines map[string]protocol.Baseline `json:"baselines"`
}

// Store serializes mutations and holds an exclusive process lock for its lifetime.
// A failed durable write makes it read-only: uncertain commits must never replay.
type Store struct {
	mu          sync.Mutex
	root        *os.Root
	lock        *localfs.Lock
	limits      Limits
	options     Options
	meta        metadata
	runs        map[string]protocol.Run
	requests    map[string]string
	annotations map[string]protocol.Annotation
	sizes       map[string]int64
	refs        map[string]int
	diagnostics []string
	readOnly    bool
	closed      bool
}

func Open(path string, limits Limits, options Options) (*Store, error) {
	if limits.MaxRuns < 1 || limits.MaxRuns > 1000 || limits.MaxBytes < 2*MaxMetadataBytes+limits.MaxRunBytes || limits.MaxBytes > 1<<30 || limits.MaxRunBytes < 1024 || limits.MaxRunBytes > 16<<20 || limits.MaxRunBytes > limits.MaxBytes || limits.MaxArtifactBytes < 1 || limits.MaxArtifactBytes > 4<<20 || limits.MaxArtifactBytes > limits.MaxRunBytes || limits.MaxReceipts < 1 || limits.MaxReceipts > 10000 {
		return nil, fault.New(protocol.InvalidConfig, "Invalid evidence storage limits.")
	}
	lock, err := localfs.Acquire(filepath.Join(path, ".lock"))
	if err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		_ = lock.Close()
		return nil, err
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	s := &Store{root: root, lock: lock, limits: limits, options: options, runs: map[string]protocol.Run{}, requests: map[string]string{}, annotations: map[string]protocol.Annotation{}, sizes: map[string]int64{}, refs: map[string]int{}, diagnostics: []string{}}
	s.meta = metadata{Version: Version, HighWater: options.Now().UTC(), Deleted: map[string]receipt{}, Baselines: map[string]protocol.Baseline{}}
	if err := s.load(); err != nil {
		_ = s.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) issue(message string) {
	s.readOnly = true
	if len(s.diagnostics) < 100 {
		s.diagnostics = append(s.diagnostics, message)
	}
}

func (s *Store) read(name string, limit int64) ([]byte, error) {
	f, err := s.root.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || !localfs.Owned(info) || info.Mode().Perm()&0077 != 0 || info.Size() > limit {
		return nil, errors.New("invalid private evidence file")
	}
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil || int64(len(data)) > limit {
		return nil, errors.New("evidence read exceeds limit")
	}
	return data, nil
}

func (s *Store) load() error {
	dir, err := s.root.Open(".")
	if err != nil {
		return err
	}
	entries, err := dir.ReadDir(40001)
	_ = dir.Close()
	if err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	if len(entries) > 40000 {
		return fault.New(protocol.StorageFull, "Too many evidence files; inspect the store offline.")
	}
	for _, e := range entries {
		if e.Name() == ".lock" {
			continue
		}
		info, err := s.root.Lstat(e.Name())
		if err != nil || !info.Mode().IsRegular() || !localfs.Owned(info) || info.Mode().Perm()&0077 != 0 {
			s.issue("Unsafe store entry is quarantined; storage is read-only.")
			continue
		}
		s.sizes[e.Name()] = info.Size()
	}
	if data, err := s.read("store.json", MaxMetadataBytes); err == nil {
		var meta metadata
		if jsonstrict.Decode(data, &meta) != nil || meta.Version != Version || meta.Deleted == nil || meta.Baselines == nil || meta.HighWater.IsZero() || len(meta.Deleted) > s.limits.MaxReceipts || !validMetadata(meta) {
			s.issue("Unsupported or corrupt store metadata; no repair or replay attempted.")
		} else {
			s.meta = meta
		}
	} else if !errors.Is(err, os.ErrNotExist) || len(s.sizes) != 0 {
		s.issue("Cannot read store metadata; no repair or replay attempted.")
	}
	if s.used() > s.limits.MaxBytes {
		s.issue("Saved data exceeds configured quota.")
		return nil
	}
	for name := range s.sizes {
		if !strings.HasPrefix(name, "run-") || !strings.HasSuffix(name, ".json") {
			continue
		}
		if len(s.runs) >= s.limits.MaxRuns {
			s.issue("Saved run count exceeds configured limit.")
			break
		}
		data, err := s.read(name, MaxManifestBytes)
		var run protocol.Run
		if err != nil || jsonstrict.Decode(data, &run) != nil || validateRun(run) != nil || name != runFile(run.ID) {
			s.issue("Corrupt or unsupported run quarantined; admissions disabled to preserve deduplication.")
			continue
		}
		if tomb, deleted := s.meta.Deleted[run.RequestID]; deleted && tomb.RunID == run.ID {
			continue
		}
		if _, duplicate := s.requests[run.RequestID]; duplicate {
			s.issue("Conflicting durable request identities.")
			continue
		}
		s.runs[run.ID], s.requests[run.RequestID] = run, run.ID
		if len(s.runs) > s.limits.MaxRuns {
			s.issue("Saved run count exceeds configured limit.")
		}
		if data, err := s.read(annotationFile(run.ID), 16384); err == nil {
			var annotation protocol.Annotation
			if jsonstrict.Decode(data, &annotation) != nil || len(annotation.Title) > 256 || len(annotation.Note) > 8192 {
				s.issue("Invalid annotation quarantined.")
			} else {
				s.annotations[run.ID] = annotation
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			s.issue("Cannot read a saved annotation.")
		}
		for _, a := range run.Artifacts {
			data, err := s.read(artifactFile(run.ID, a.ID), s.limits.MaxArtifactBytes)
			if err != nil || int64(len(data)) != a.Size || Digest(data) != a.SHA256 {
				s.issue("Artifact integrity failure; evidence is read-only.")
			}
		}
	}
	for _, b := range s.meta.Baselines {
		if b.RunID != "" {
			run, ok := s.runs[b.RunID]
			if !ok || run.State != "success" || run.Project != b.Project || run.Experiment.ID != b.Experiment {
				s.issue("Baseline references missing or incompatible evidence.")
			}
		}
	}
	if s.used() > s.limits.MaxBytes {
		s.issue("Saved data exceeds configured quota.")
	}
	if s.options.Now().Before(s.meta.HighWater) {
		s.issue("Clock rollback detected; admissions disabled until safe restart.")
	}
	if s.readOnly {
		return nil
	}
	// Recover by marking interrupted; never reconstruct or resume a provider call.
	for id, run := range s.runs {
		if Terminal(run.State) {
			continue
		}
		now := s.options.Now().UTC()
		run.State, run.FinishedAt = "interrupted", &now
		run.Error = fault.New(protocol.ExecutionFailed, "Daemon stopped before durable completion; external effects may be unknown.")
		if err := s.write(runFile(id), run, MaxManifestBytes); err != nil {
			return nil
		}
		s.runs[id] = run
	}
	if _, exists := s.sizes["store.json"]; !exists {
		return s.writeMeta(s.meta)
	}
	return nil
}

func runFile(id string) string           { return "run-" + id + ".json" }
func annotationFile(id string) string    { return "annotation-" + id + ".json" }
func artifactFile(run, id string) string { return "artifact-" + run + "-" + id + ".json" }

func (s *Store) used() int64 {
	var n int64
	for _, size := range s.sizes {
		n += size
	}
	return n
}
func (s *Store) runBytes(id string) int64 {
	n := s.sizes[runFile(id)] + s.sizes[annotationFile(id)]
	for name, size := range s.sizes {
		if strings.HasPrefix(name, "artifact-"+id+"-") {
			n += size
		}
	}
	return n
}
func (s *Store) reserved() int64 {
	var n int64
	for id, r := range s.runs {
		if !Terminal(r.State) {
			n += max(0, s.limits.MaxRunBytes-s.runBytes(id))
		}
	}
	return n
}
func (s *Store) writable() error {
	if s.closed {
		return fault.New(protocol.ShuttingDown, "Evidence store is closed.")
	}
	if s.readOnly {
		return fault.New(protocol.RecordingFailed, "Evidence store is read-only; inspect storage diagnostics.")
	}
	if s.options.Now().Before(s.meta.HighWater) {
		s.issue("Clock rollback detected.")
		return fault.New(protocol.RecordingFailed, "Clock rollback prevents new evidence writes.")
	}
	return nil
}

func (s *Store) inject(op, name string) error {
	if s.options.Fault != nil {
		return s.options.Fault(op, name)
	}
	return nil
}

// write uses an exclusive private temporary file and a directory fsync. Any
// failure after a rename is an uncertain commit, so further mutations fail closed.
func (s *Store) write(name string, value any, limit int64) error {
	data, err := json.Marshal(value)
	if err != nil || int64(len(data)) > limit {
		return fault.New(protocol.StorageFull, "Evidence record exceeds its size limit.")
	}
	return s.writeBytes(name, data)
}
func (s *Store) writeBytes(name string, data []byte) (result error) {
	if !s.fits(name, int64(len(data))) {
		return fault.New(protocol.StorageFull, "Evidence storage has insufficient staging space.")
	}
	tmp := ".stage-" + identity.New()
	f, err := s.root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		s.issue("Cannot create evidence staging file.")
		return fault.New(protocol.RecordingFailed, "Cannot create evidence record.")
	}
	defer func() {
		_ = f.Close()
		_ = s.root.Remove(tmp)
		if result != nil {
			s.issue("Durable evidence write failed; inspect before resuming.")
		}
	}()
	if err := s.inject("write", name); err != nil {
		return fault.New(protocol.RecordingFailed, "Cannot write evidence record.")
	}
	if _, err := f.Write(data); err != nil {
		return fault.New(protocol.RecordingFailed, "Cannot write evidence record.")
	}
	if err := s.inject("sync", name); err != nil {
		return fault.New(protocol.RecordingFailed, "Cannot sync evidence record.")
	}
	if err := f.Sync(); err != nil {
		return fault.New(protocol.RecordingFailed, "Cannot sync evidence record.")
	}
	if err := f.Close(); err != nil {
		return fault.New(protocol.RecordingFailed, "Cannot close evidence record.")
	}
	if err := s.inject("rename", name); err != nil {
		return fault.New(protocol.RecordingFailed, "Cannot commit evidence record.")
	}
	if err := s.root.Rename(tmp, name); err != nil {
		return fault.New(protocol.RecordingFailed, "Cannot commit evidence record.")
	}
	s.sizes[name] = int64(len(data))
	if err := s.inject("directory_sync", name); err != nil {
		return fault.New(protocol.RecordingFailed, "Evidence commit durability is uncertain.")
	}
	dir, err := s.root.Open(".")
	if err != nil {
		return fault.New(protocol.RecordingFailed, "Cannot sync evidence directory.")
	}
	defer func() { _ = dir.Close() }()
	if err := dir.Sync(); err != nil {
		return fault.New(protocol.RecordingFailed, "Evidence commit durability is uncertain.")
	}
	return nil
}

func (s *Store) writeMeta(next metadata) error {
	next.HighWater = maxTime(next.HighWater, s.options.Now().UTC())
	if err := s.write("store.json", next, MaxMetadataBytes); err != nil {
		return err
	}
	s.meta = next
	return nil
}

func (s *Store) lookup(request, digest string) (protocol.CaptureResponse, bool, error) {
	if id, ok := s.requests[request]; ok {
		r := s.runs[id]
		if r.RequestDigest != digest {
			return protocol.CaptureResponse{}, true, fault.New(protocol.RequestConflict, "Request ID was already used for different content.")
		}
		return protocol.CaptureResponse{RunID: id, JobID: r.JobID}, true, nil
	}
	if receipt, ok := s.meta.Deleted[request]; ok {
		if receipt.Digest != digest {
			return protocol.CaptureResponse{}, true, fault.New(protocol.RequestConflict, "Request ID was already used for different content.")
		}
		return protocol.CaptureResponse{RunID: receipt.RunID, JobID: receipt.JobID, Deleted: true}, true, nil
	}
	return protocol.CaptureResponse{}, false, nil
}
func (s *Store) Lookup(request, digest string) (protocol.CaptureResponse, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lookup(request, digest)
}

func (s *Store) Reserve(run protocol.Run) (protocol.CaptureResponse, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if value, found, err := s.lookup(run.RequestID, run.RequestDigest); found {
		return value, true, err
	}
	if err := s.writable(); err != nil {
		return protocol.CaptureResponse{}, false, err
	}
	now := s.options.Now().UTC()
	stamp, err := RequestTime(run.RequestID)
	if err != nil {
		return protocol.CaptureResponse{}, false, err
	}
	if stamp.Before(now.Add(-24*time.Hour)) || stamp.After(now.Add(5*time.Minute)) {
		return protocol.CaptureResponse{}, false, fault.New(protocol.InvalidRequest, "Request ID is outside the admission window.")
	}
	if len(s.runs) >= s.limits.MaxRuns || s.used()-s.sizes["store.json"]+s.reserved()+s.limits.MaxRunBytes+2*MaxMetadataBytes > s.limits.MaxBytes {
		return protocol.CaptureResponse{}, false, fault.New(protocol.StorageFull, "Run quota is full; explicitly remove saved runs or change limits.")
	}
	next := Clone(s.meta)
	for key, receipt := range next.Deleted {
		if !now.Before(receipt.Expires) && !s.hasRunFiles(receipt.RunID) {
			delete(next.Deleted, key)
		}
	}
	if len(next.Deleted) >= s.limits.MaxReceipts {
		return protocol.CaptureResponse{}, false, fault.New(protocol.StorageFull, "Request receipt quota is full.")
	}
	next.HighWater = now
	if err := s.writeMeta(next); err != nil {
		return protocol.CaptureResponse{}, false, err
	}
	run.ID, run.SchemaVersion, run.Origin, run.State, run.CreatedAt = identity.New(), Version, "measured", "queued", now
	run.StartedAt, run.FinishedAt, run.Error = nil, nil, nil
	if run.Steps == nil {
		run.Steps = []protocol.PreparedStep{}
	}
	if run.Experiment.Collectors == nil {
		run.Experiment.Collectors = []protocol.Collector{}
	}
	if run.Parameters == nil {
		run.Parameters = map[string]any{}
	}
	run.Measurements, run.Artifacts, run.Annotation = []protocol.Measurement{}, []protocol.Artifact{}, protocol.Annotation{}
	if err := validateRun(run); err != nil {
		return protocol.CaptureResponse{}, false, err
	}
	data, err := json.Marshal(run)
	if err != nil {
		return protocol.CaptureResponse{}, false, fault.New(protocol.Internal, "Cannot encode prepared run.")
	}
	if int64(len(data)) > min(s.limits.MaxRunBytes, MaxManifestBytes) {
		return protocol.CaptureResponse{}, false, fault.New(protocol.StorageFull, "Prepared run exceeds its storage allowance.")
	}
	if err := s.writeBytes(runFile(run.ID), data); err != nil {
		return protocol.CaptureResponse{}, false, err
	}
	s.runs[run.ID], s.requests[run.RequestID] = Clone(run), run.ID
	return protocol.CaptureResponse{RunID: run.ID, JobID: run.JobID}, false, nil
}

func (s *Store) Get(id string) (protocol.Run, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	run, ok := s.runs[id]
	if !ok {
		return protocol.Run{}, fault.New(protocol.NotFound, "Saved run not found.")
	}
	run.Annotation = s.annotations[id]
	return Clone(run), nil
}

func (s *Store) Update(run protocol.Run) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.writable(); err != nil {
		return err
	}
	old, ok := s.runs[run.ID]
	if !ok {
		return fault.New(protocol.NotFound, "Saved run not found.")
	}
	if Terminal(old.State) || run.RequestID != old.RequestID || run.RequestDigest != old.RequestDigest || run.PlanDigest != old.PlanDigest || run.Project != old.Project || run.ExperimentDigest != old.ExperimentDigest || !run.CreatedAt.Equal(old.CreatedAt) || old.State == "running" && run.State == "queued" {
		return fault.New(protocol.RequestConflict, "Run evidence or terminal outcome cannot be replaced.")
	}
	if !sameJSON(immutable(old), immutable(run)) || !sameJSON(old.Artifacts, run.Artifacts) {
		return fault.New(protocol.RequestConflict, "Prepared evidence and artifacts are immutable.")
	}
	if err := validateRun(run); err != nil {
		return err
	}
	run.Annotation = protocol.Annotation{}
	data, err := json.Marshal(run)
	if err != nil || len(data) > MaxManifestBytes || s.runBytes(run.ID)-s.sizes[runFile(run.ID)]+int64(len(data)) > s.limits.MaxRunBytes {
		return fault.New(protocol.StorageFull, "Run exceeds its evidence limit.")
	}
	if err := s.writeBytes(runFile(run.ID), data); err != nil {
		return err
	}
	s.runs[run.ID] = Clone(run)
	return nil
}

func (s *Store) AddArtifact(id, name, media string, data []byte) (protocol.Artifact, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.writable(); err != nil {
		return protocol.Artifact{}, err
	}
	run, ok := s.runs[id]
	if !ok {
		return protocol.Artifact{}, fault.New(protocol.NotFound, "Saved run not found.")
	}
	if Terminal(run.State) || len(run.Artifacts) >= MaxArtifacts || len(name) == 0 || len(name) > 128 || media != "application/json" && media != "text/plain" {
		return protocol.Artifact{}, fault.New(protocol.InvalidRequest, "Unsupported artifact or terminal run.")
	}
	if int64(len(data)) > s.limits.MaxArtifactBytes || s.runBytes(id)+int64(len(data))+2048 > s.limits.MaxRunBytes {
		return protocol.Artifact{}, fault.New(protocol.StorageFull, "Artifact exceeds run limits.")
	}
	a := protocol.Artifact{SchemaVersion: Version, ID: identity.New(), Name: name, MediaType: media, Size: int64(len(data)), SHA256: Digest(data)}
	if err := s.writeBytes(artifactFile(id, a.ID), data); err != nil {
		return protocol.Artifact{}, err
	}
	run.Artifacts = append(run.Artifacts, a)
	if err := s.write(runFile(id), run, MaxManifestBytes); err != nil {
		return protocol.Artifact{}, err
	}
	s.runs[id] = run
	return a, nil
}

func (s *Store) Artifact(id, artifact string) ([]byte, protocol.Artifact, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	run, ok := s.runs[id]
	if !ok {
		return nil, protocol.Artifact{}, fault.New(protocol.NotFound, "Run not found.")
	}
	for _, a := range run.Artifacts {
		if a.ID != artifact {
			continue
		}
		data, err := s.read(artifactFile(id, a.ID), s.limits.MaxArtifactBytes)
		if err != nil || int64(len(data)) != a.Size || Digest(data) != a.SHA256 {
			return nil, a, fault.New(protocol.RecordingFailed, "Artifact integrity check failed.")
		}
		return data, a, nil
	}
	return nil, protocol.Artifact{}, fault.New(protocol.NotFound, "Artifact not found.")
}

func (s *Store) List(project, experiment, cursor string, limit int) (protocol.RunList, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := protocol.RunList{Runs: []protocol.RunSummary{}}
	if limit == 0 {
		limit = 50
	}
	if limit < 1 || limit > 100 {
		return result, fault.New(protocol.InvalidRequest, "Page size must be between 1 and 100.")
	}
	var after struct {
		Time       time.Time `json:"time"`
		ID         string    `json:"id"`
		Project    string    `json:"project"`
		Experiment string    `json:"experiment"`
	}
	if cursor != "" {
		data, err := base64.RawURLEncoding.DecodeString(cursor)
		if err != nil || len(data) > 1024 || jsonstrict.Decode(data, &after) != nil || !identifier.MatchString(after.ID) || after.Time.IsZero() || after.Project != project || after.Experiment != experiment {
			return result, fault.New(protocol.InvalidRequest, "Invalid page cursor.")
		}
	}
	var runs []protocol.Run
	for _, r := range s.runs {
		if project != "" && r.Project != project || experiment != "" && r.Experiment.ID != experiment {
			continue
		}
		if cursor != "" && (r.CreatedAt.After(after.Time) || r.CreatedAt.Equal(after.Time) && r.ID <= after.ID) {
			continue
		}
		runs = append(runs, r)
	}
	sort.Slice(runs, func(i, j int) bool {
		if runs[i].CreatedAt.Equal(runs[j].CreatedAt) {
			return runs[i].ID < runs[j].ID
		}
		return runs[i].CreatedAt.After(runs[j].CreatedAt)
	})
	for _, r := range runs[:min(limit, len(runs))] {
		result.Runs = append(result.Runs, protocol.RunSummary{ID: r.ID, Experiment: r.Experiment.ID, Project: r.Project, State: r.State, CreatedAt: r.CreatedAt, JobID: r.JobID, Annotation: s.annotations[r.ID]})
	}
	if len(runs) > limit {
		last := runs[limit-1]
		after.Time, after.ID, after.Project, after.Experiment = last.CreatedAt, last.ID, project, experiment
		data, _ := json.Marshal(after)
		result.NextCursor = base64.RawURLEncoding.EncodeToString(data)
	}
	return result, nil
}

func (s *Store) Annotate(id string, update protocol.AnnotationUpdate) (protocol.Annotation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.writable(); err != nil {
		return protocol.Annotation{}, err
	}
	if _, ok := s.runs[id]; !ok {
		return protocol.Annotation{}, fault.New(protocol.NotFound, "Run not found.")
	}
	old := s.annotations[id]
	if old.Revision != update.Revision {
		return old, fault.New(protocol.RequestConflict, "Annotation changed; refresh before editing.")
	}
	if len(update.Title) > 256 || len(update.Note) > 8192 {
		return old, fault.New(protocol.InvalidRequest, "Annotation exceeds its text limit.")
	}
	next := protocol.Annotation{Revision: old.Revision + 1, Title: update.Title, Note: update.Note, Pinned: update.Pinned, Updated: s.options.Now().UTC()}
	data, _ := json.Marshal(next)
	if s.runBytes(id)-s.sizes[annotationFile(id)]+int64(len(data)) > s.limits.MaxRunBytes {
		return old, fault.New(protocol.StorageFull, "Annotation exceeds the run limit.")
	}
	if err := s.write(annotationFile(id), next, 16384); err != nil {
		return old, err
	}
	s.annotations[id] = next
	return next, nil
}

func baselineKey(project, experiment string) string { return project + "/" + experiment }
func (s *Store) Baseline(project, experiment string) protocol.Baseline {
	s.mu.Lock()
	defer s.mu.Unlock()
	value := s.meta.Baselines[baselineKey(project, experiment)]
	value.Project, value.Experiment = project, experiment
	return value
}
func (s *Store) SetBaseline(project, experiment string, update protocol.BaselineUpdate) (protocol.Baseline, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.writable(); err != nil {
		return protocol.Baseline{}, err
	}
	key := baselineKey(project, experiment)
	old := s.meta.Baselines[key]
	if len(s.meta.Baselines) >= 1000 && old.Revision == 0 {
		return old, fault.New(protocol.StorageFull, "Baseline quota is full.")
	}
	if old.Revision != update.Revision {
		return old, fault.New(protocol.RequestConflict, "Baseline changed; refresh first.")
	}
	if update.RunID != "" {
		r, ok := s.runs[update.RunID]
		if !ok || r.Project != project || r.Experiment.ID != experiment || r.State != "success" {
			return old, fault.New(protocol.IncompatibleResults, "Baseline must be a successful run of this project and experiment.")
		}
	}
	next := Clone(s.meta)
	value := protocol.Baseline{Project: project, Experiment: experiment, RunID: update.RunID, Revision: old.Revision + 1}
	next.Baselines[key] = value
	if err := s.writeMeta(next); err != nil {
		return old, err
	}
	return value, nil
}

func (s *Store) Hold(ids ...string) (func(), error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, id := range ids {
		if _, ok := s.runs[id]; !ok {
			return nil, fault.New(protocol.NotFound, "Run not found.")
		}
	}
	for _, id := range ids {
		s.refs[id]++
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			s.mu.Lock()
			defer s.mu.Unlock()
			for _, id := range ids {
				s.refs[id]--
			}
		})
	}, nil
}

func (s *Store) Delete(id string, acknowledge bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.writable(); err != nil {
		return err
	}
	r, ok := s.runs[id]
	if !ok {
		return fault.New(protocol.NotFound, "Run not found.")
	}
	if !Terminal(r.State) || s.refs[id] > 0 {
		return fault.New(protocol.RequestConflict, "Run is active or in use.")
	}
	if s.annotations[id].Pinned {
		return fault.New(protocol.RequestConflict, "Unpin the run before deleting it.")
	}
	next := Clone(s.meta)
	for key, b := range next.Baselines {
		if b.RunID == id {
			if !acknowledge {
				return fault.New(protocol.ConfirmationRequired, "Deleting this baseline requires explicit acknowledgement.")
			}
			b.RunID = ""
			b.Revision++
			next.Baselines[key] = b
		}
	}
	if len(next.Deleted) >= s.limits.MaxReceipts {
		return fault.New(protocol.StorageFull, "Request receipt quota is full.")
	}
	next.Deleted[r.RequestID] = receipt{Digest: r.RequestDigest, RunID: id, JobID: r.JobID, Expires: r.CreatedAt.Add(25 * time.Hour)}
	if err := s.writeMeta(next); err != nil {
		return err
	}
	// The durable tombstone makes a crash during cleanup a deleted record, never a replay.
	delete(s.requests, r.RequestID)
	delete(s.runs, id)
	delete(s.annotations, id)
	names := []string{runFile(id), annotationFile(id)}
	for _, a := range r.Artifacts {
		names = append(names, artifactFile(id, a.ID))
	}
	for _, name := range names {
		if _, exists := s.sizes[name]; !exists {
			continue
		}
		info, err := s.root.Lstat(name)
		if err != nil || !info.Mode().IsRegular() {
			s.issue("Deleted evidence requires manual cleanup.")
			return fault.New(protocol.RecordingFailed, "Deleted record retained for safe cleanup.")
		}
		if err := s.root.Remove(name); err != nil {
			s.issue("Deleted evidence cleanup failed.")
			return fault.New(protocol.RecordingFailed, "Deleted record retained for safe cleanup.")
		}
		delete(s.sizes, name)
	}
	dir, err := s.root.Open(".")
	if err != nil {
		s.issue("Deletion durability uncertain.")
		return fault.New(protocol.RecordingFailed, "Cannot sync deletion.")
	}
	defer func() { _ = dir.Close() }()
	if err := dir.Sync(); err != nil {
		s.issue("Deletion durability uncertain.")
		return fault.New(protocol.RecordingFailed, "Cannot sync deletion.")
	}
	return nil
}

func (s *Store) Status() protocol.StoreStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	return protocol.StoreStatus{Available: !s.closed, ReadOnly: s.readOnly, Runs: len(s.runs), Bytes: s.used(), Reserved: s.reserved(), MaxBytes: s.limits.MaxBytes, MaxRuns: s.limits.MaxRuns, Diagnostics: append([]string{}, s.diagnostics...)}
}
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	return errors.Join(s.root.Close(), s.lock.Close())
}

// Keep room for the largest metadata image and one atomic staging image. A run
// consumes its own reservation; edits to completed runs cannot consume another's.
func (s *Store) fits(name string, size int64) bool {
	used := s.used() - s.sizes["store.json"]
	reserved := s.reserved()
	delta := size - s.sizes[name]
	if name != "store.json" {
		used += delta
	}
	for id, run := range s.runs {
		if !Terminal(run.State) && (name == runFile(id) || name == annotationFile(id) || strings.HasPrefix(name, "artifact-"+id+"-")) {
			before := max(0, s.limits.MaxRunBytes-s.runBytes(id))
			after := max(0, s.limits.MaxRunBytes-s.runBytes(id)-delta)
			reserved += after - before
			break
		}
	}
	return used+reserved+2*MaxMetadataBytes <= s.limits.MaxBytes
}
func (s *Store) hasRunFiles(id string) bool {
	for name := range s.sizes {
		if name == runFile(id) || name == annotationFile(id) || strings.HasPrefix(name, "artifact-"+id+"-") {
			return true
		}
	}
	return false
}
func maxTime(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}
func sameJSON(a, b any) bool {
	left, _ := json.Marshal(a)
	right, _ := json.Marshal(b)
	return bytes.Equal(left, right)
}
func immutable(run protocol.Run) any {
	return struct {
		ID, Origin, Project, Instance, JobID, RequestID, RequestDigest, PlanDigest, ExperimentDigest string
		Version                                                                                      int
		Generation                                                                                   uint64
		Created                                                                                      time.Time
		Experiment                                                                                   protocol.Experiment
		Parameters                                                                                   map[string]any
		Steps                                                                                        []protocol.PreparedStep
	}{run.ID, run.Origin, run.Project, run.Instance, run.JobID, run.RequestID, run.RequestDigest, run.PlanDigest, run.ExperimentDigest, run.SchemaVersion, run.Generation, run.CreatedAt, run.Experiment, run.Parameters, run.Steps}
}

func validMetadata(meta metadata) bool {
	if len(meta.Baselines) > 1000 {
		return false
	}
	for id, tomb := range meta.Deleted {
		if _, err := RequestTime(id); err != nil || !identifier.MatchString(tomb.RunID) || !digestPattern.MatchString(tomb.Digest) || tomb.Expires.IsZero() {
			return false
		}
	}
	for key, b := range meta.Baselines {
		if key != baselineKey(b.Project, b.Experiment) || b.Experiment == "" || b.Revision == 0 || b.RunID != "" && !identifier.MatchString(b.RunID) {
			return false
		}
	}
	return true
}
