package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/enowdev/enowx-rag/mcp-server/pkg/core"
	"github.com/go-chi/chi/v5"
)

// Job is a long-running server task (re-index, re-embed, import) the
// dashboard follows with GET /api/jobs/{id} and job_* events.
type Job struct {
	ID         string         `json:"id"`
	Kind       string         `json:"kind"` // reindex | reembed | import | refresh
	Project    string         `json:"project,omitempty"`
	State      string         `json:"state"` // running | done | failed
	Message    string         `json:"message"`
	Done       int            `json:"done"`
	Total      int            `json:"total"`
	StartedAt  time.Time      `json:"started_at"`
	FinishedAt *time.Time     `json:"finished_at,omitempty"`
	Result     map[string]any `json:"result,omitempty"`
	Error      string         `json:"error,omitempty"`
}

// JobUpdate lets a running job report progress.
type JobUpdate func(done, total int, message string)

// Jobs runs and remembers jobs (the last maxJobs).
type Jobs struct {
	mu   sync.Mutex
	list []*Job
	bus  *core.EventBus
}

const maxJobs = 50

// NewJobs publishes job events on bus.
func NewJobs(bus *core.EventBus) *Jobs { return &Jobs{bus: bus} }

func newJobID() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return "j_" + hex.EncodeToString(b)
}

// Running returns the running job of kind for project, if any ("" kind = any).
func (j *Jobs) Running(kind, project string) *Job {
	j.mu.Lock()
	defer j.mu.Unlock()
	for _, job := range j.list {
		if job.State == "running" && job.Project == project && (kind == "" || job.Kind == kind) {
			c := *job
			return &c
		}
	}
	return nil
}

// RunningCount is how many jobs are running.
func (j *Jobs) RunningCount() int {
	j.mu.Lock()
	defer j.mu.Unlock()
	n := 0
	for _, job := range j.list {
		if job.State == "running" {
			n++
		}
	}
	return n
}

// JobFunc is a job's work; update reports progress.
type JobFunc func(ctx context.Context, update JobUpdate) (map[string]any, error)

// Start runs fn in the background as a new job and returns its first state.
func (j *Jobs) Start(kind, project, message string, fn JobFunc) Job {
	job, first := j.add(kind, project, message)
	go j.run(job, fn)
	return first
}

// RunSync runs fn as a new job and returns its final state when it is done.
func (j *Jobs) RunSync(kind, project, message string, fn JobFunc) Job {
	job, _ := j.add(kind, project, message)
	j.run(job, fn)
	return j.snapshot(job)
}

func (j *Jobs) add(kind, project, message string) (*Job, Job) {
	job := &Job{ID: newJobID(), Kind: kind, Project: project, State: "running", Message: message, StartedAt: time.Now().UTC()}
	j.mu.Lock()
	j.list = append([]*Job{job}, j.list...)
	if len(j.list) > maxJobs {
		j.list = j.list[:maxJobs]
	}
	first := *job
	j.mu.Unlock()
	j.publish("job_started", job)
	return job, first
}

func (j *Jobs) run(job *Job, fn JobFunc) {
	{
		var lastPublish time.Time
		update := func(done, total int, msg string) {
			j.mu.Lock()
			job.Done, job.Total = done, total
			if msg != "" {
				job.Message = msg
			}
			j.mu.Unlock()
			// Throttle progress events so a big job doesn't flood the bus.
			if time.Since(lastPublish) > 400*time.Millisecond || done == total {
				lastPublish = time.Now()
				j.publish("job_progress", job)
			}
		}
		result, err := func() (res map[string]any, err error) {
			defer func() {
				if r := recover(); r != nil {
					err = fmt.Errorf("job crashed: %v", r)
				}
			}()
			return fn(context.Background(), update)
		}()
		now := time.Now().UTC()
		j.mu.Lock()
		job.FinishedAt = &now
		job.Result = result
		if err != nil {
			job.State, job.Error = "failed", err.Error()
		} else {
			job.State = "done"
			if job.Total > 0 {
				job.Done = job.Total
			}
		}
		j.mu.Unlock()
		if err != nil {
			j.publish("job_failed", job)
		} else {
			j.publish("job_completed", job)
		}
	}
}

func (j *Jobs) snapshot(job *Job) Job {
	j.mu.Lock()
	defer j.mu.Unlock()
	return *job
}

func (j *Jobs) publish(typ string, job *Job) {
	if j.bus == nil {
		return
	}
	c := j.snapshot(job)
	data := map[string]any{
		"id": c.ID, "kind": c.Kind, "project": c.Project, "project_id": c.Project, "state": c.State, "message": c.Message,
		"done": c.Done, "total": c.Total, "started_at": c.StartedAt, "result": c.Result, "error": c.Error,
	}
	if c.FinishedAt != nil {
		data["finished_at"] = *c.FinishedAt
	}
	j.bus.Publish(core.Event{Type: typ, Timestamp: time.Now(), Data: data})
}

// List returns the jobs, newest first.
func (j *Jobs) List() []Job {
	j.mu.Lock()
	defer j.mu.Unlock()
	out := make([]Job, len(j.list))
	for i, job := range j.list {
		out[i] = *job
	}
	return out
}

// Get returns one job.
func (j *Jobs) Get(id string) (Job, bool) {
	j.mu.Lock()
	defer j.mu.Unlock()
	for _, job := range j.list {
		if job.ID == id {
			return *job, true
		}
	}
	return Job{}, false
}

// ListJobs handles GET /api/jobs.
func (h *Handlers) ListJobs(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, h.jobs.List())
}

// GetJob handles GET /api/jobs/{id}.
func (h *Handlers) GetJob(w http.ResponseWriter, r *http.Request) {
	job, ok := h.jobs.Get(chi.URLParam(r, "id"))
	if !ok {
		writeErr(w, http.StatusNotFound, "job not found")
		return
	}
	writeJSON(w, http.StatusOK, job)
}
