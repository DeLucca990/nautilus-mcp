package bazarr

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	SourceRadarr = "radarr"
	SourceSonarr = "sonarr"
)

const (
	LibraryAlreadyThere = "already_there"
	LibraryArrived      = "arrived"
	LibraryStillMissing = "still_missing"
	LibraryPending      = "pending"
	LibraryStarted      = "started"
)

var libraryTasks = map[string]string{
	SourceRadarr: "update_movies",
	SourceSonarr: "update_series",
}

var (
	libraryPoll = 2 * time.Second
	libraryWait = 90 * time.Second
)

type LibrarySyncRequest struct {
	Source   string
	RadarrID int
	SeriesID int
}

type LibrarySync struct {
	Source   string `json:"source" jsonschema:"radarr or sonarr: the library Bazarr copied"`
	Task     string `json:"task" jsonschema:"the Bazarr task, as its System → Tasks page names it"`
	Interval string `json:"interval,omitempty" jsonschema:"how often Bazarr runs it on its own"`

	Outcome        string `json:"outcome" jsonschema:"already_there: the item was in Bazarr and nothing was started; arrived: it is there now; still_missing: the sync finished without it; pending: it is not there yet and the sync may still be running; started: no item was named, and the sync runs in the background"`
	AlreadyRunning bool   `json:"already_running,omitempty" jsonschema:"the sync was already under way, so it was left to finish rather than started again"`
	WaitedSeconds  int    `json:"waited_seconds,omitempty"`

	RadarrID int     `json:"radarr_id,omitempty"`
	SeriesID int     `json:"series_id,omitempty"`
	Movie    *Movie  `json:"movie,omitempty" jsonschema:"the movie asked for, as Bazarr has it now"`
	Series   *Series `json:"series,omitempty" jsonschema:"the series asked for, as Bazarr has it now"`

	Warnings []string `json:"warnings,omitempty"`
}

func SyncLibrary(ctx context.Context, req LibrarySyncRequest) (LibrarySync, error) {
	if err := req.resolve(); err != nil {
		return LibrarySync{}, err
	}
	c, err := newClient()
	if err != nil {
		return LibrarySync{}, err
	}

	id := libraryTasks[req.Source]
	t, ok, err := c.task(ctx, id)
	if err != nil {
		return LibrarySync{}, err
	}
	if !ok {
		name := sourceName(req.Source)
		return LibrarySync{}, fmt.Errorf("bazarr has no %s task, so it is not using %s: it copies "+
			"that library only while Settings → %s is enabled", id, name, name)
	}

	out := LibrarySync{
		Source:   req.Source,
		Task:     t.Name,
		Interval: t.Interval,
		RadarrID: req.RadarrID,
		SeriesID: req.SeriesID,
	}

	found, err := c.lookup(ctx, req, &out)
	if err != nil {
		return LibrarySync{}, err
	}
	if found {
		out.Outcome = LibraryAlreadyThere
		return out, nil
	}

	out.AlreadyRunning = t.Running
	if !t.Running {
		form := url.Values{"taskid": {id}}
		if err := c.send(ctx, http.MethodPost, "/system/tasks", form, requestTimeout); err != nil {
			return LibrarySync{}, err
		}
	}
	if req.RadarrID == 0 && req.SeriesID == 0 {
		out.Outcome = LibraryStarted
		return out, nil
	}

	start := time.Now()
	seenRunning := t.Running
	for out.Outcome == "" {
		select {
		case <-ctx.Done():
			return LibrarySync{}, ctx.Err()
		case <-time.After(libraryPoll):
		}

		now, listed, taskErr := c.task(ctx, id)
		found, err := c.lookup(ctx, req, &out)
		if err != nil {
			return LibrarySync{}, err
		}
		out.WaitedSeconds = int(time.Since(start).Seconds())

		switch {
		case found:
			out.Outcome = LibraryArrived
		case taskErr == nil && listed && now.Running:
			seenRunning = true
		case taskErr == nil && listed && seenRunning:
			out.Outcome = LibraryStillMissing
		}
		if out.Outcome == "" && time.Since(start) >= libraryWait {
			out.Outcome = LibraryPending
		}
	}

	out.Warnings = libraryWarnings(req, out.Outcome)
	return out, nil
}

func (r *LibrarySyncRequest) resolve() error {
	r.Source = strings.ToLower(strings.TrimSpace(r.Source))
	switch {
	case r.RadarrID < 0 || r.SeriesID < 0:
		return fmt.Errorf("ids are positive numbers")
	case r.RadarrID > 0 && r.SeriesID > 0:
		return fmt.Errorf("a sync copies one library: pass 'radarr_id' or 'series_id', not both")
	case r.Source == "" && r.RadarrID > 0:
		r.Source = SourceRadarr
	case r.Source == "" && r.SeriesID > 0:
		r.Source = SourceSonarr
	}

	switch {
	case r.Source == "":
		return fmt.Errorf("name the library to copy: 'source' radarr or sonarr, or the " +
			"'radarr_id' or 'series_id' Bazarr is missing")
	case libraryTasks[r.Source] == "":
		return fmt.Errorf("source %q: must be radarr or sonarr", r.Source)
	case r.Source == SourceRadarr && r.SeriesID > 0:
		return fmt.Errorf("a series_id is Sonarr's: pass source 'sonarr', or leave source out")
	case r.Source == SourceSonarr && r.RadarrID > 0:
		return fmt.Errorf("a radarr_id is Radarr's: pass source 'radarr', or leave source out")
	}
	return nil
}

type taskJSON struct {
	JobID    string `json:"job_id"`
	Name     string `json:"name"`
	Interval string `json:"interval"`
	Running  bool   `json:"job_running"`
}

func (c *client) task(ctx context.Context, id string) (taskJSON, bool, error) {
	var page struct {
		Data []taskJSON `json:"data"`
	}
	if err := c.get(ctx, "/system/tasks", nil, &page); err != nil {
		return taskJSON{}, false, err
	}
	for _, t := range page.Data {
		if t.JobID == id {
			return t, true, nil
		}
	}
	return taskJSON{}, false, nil
}

func (c *client) lookup(ctx context.Context, req LibrarySyncRequest, out *LibrarySync) (bool, error) {
	switch {
	case req.RadarrID > 0:
		m, ok, err := c.findMovie(ctx, req.RadarrID)
		if ok {
			c.nameProfiles(ctx, &m, nil)
			out.Movie = &m
		}
		return ok, err
	case req.SeriesID > 0:
		s, ok, err := c.findSeries(ctx, req.SeriesID)
		if ok {
			c.nameProfiles(ctx, nil, &s)
			out.Series = &s
		}
		return ok, err
	}
	return false, nil
}

func libraryWarnings(req LibrarySyncRequest, outcome string) []string {
	if outcome != LibraryStillMissing && outcome != LibraryPending {
		return nil
	}

	var out []string
	if outcome == LibraryPending {
		out = append(out, "the sync may still be running — bazarr_subtitle_status in a minute "+
			"says whether it arrived")
	}
	if req.RadarrID > 0 {
		out = append(out,
			"Bazarr copies only films that have a file: one Radarr has not downloaded arrives "+
				"when its download is imported, however often this runs — radarr_library_status "+
				"says whether it has one",
			fmt.Sprintf("if %d came from a search or a link it may be the TMDB id, which Bazarr "+
				"does not use — radarr_library_status shows both numbers", req.RadarrID))
	} else {
		out = append(out, fmt.Sprintf("if %d came from a search or a link it may be the TVDB id, "+
			"which Bazarr does not use — sonarr_library_status shows both numbers", req.SeriesID))
	}
	if outcome == LibraryStillMissing {
		out = append(out, fmt.Sprintf("if %s has it under that id, bazarr_system_health shows "+
			"whether Bazarr's link to %s is working", sourceName(req.Source), sourceName(req.Source)))
	}
	return out
}

func sourceName(source string) string {
	if source == SourceSonarr {
		return "Sonarr"
	}
	return "Radarr"
}
