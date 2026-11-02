package bazarr

import (
	"context"
	"strings"
	"testing"
	"time"
)

func fastLibraryWait(t *testing.T) {
	t.Helper()
	poll, wait := libraryPoll, libraryWait
	libraryPoll, libraryWait = time.Millisecond, 50*time.Millisecond
	t.Cleanup(func() { libraryPoll, libraryWait = poll, wait })
}

func TestLibrarySyncWaitsForTheMovieToArrive(t *testing.T) {
	fastLibraryWait(t)
	m := newMockBazarr(t)
	m.onRunTask = func(taskID string) {
		if taskID == "update_movies" {
			m.movies[42] = dune(1)
		}
	}

	out, err := SyncLibrary(context.Background(), LibrarySyncRequest{RadarrID: 42})
	if err != nil {
		t.Fatal(err)
	}
	if form, ok := m.sent("POST /api/system/tasks"); !ok || form.Get("taskid") != "update_movies" {
		t.Fatalf("the Radarr sync was not started: %v", form)
	}
	if out.Outcome != LibraryArrived || out.Source != SourceRadarr || out.Task != "Sync with Radarr" {
		t.Errorf("got %+v", out)
	}
	if out.Movie == nil || out.Movie.Title != "Dune" || out.Movie.ProfileName != "PT+EN" {
		t.Errorf("the movie that arrived is not in the result: %+v", out.Movie)
	}
	if len(out.Warnings) != 0 {
		t.Errorf("warnings for a movie that arrived: %v", out.Warnings)
	}
}

func TestLibrarySyncSaysWhenTheSyncEndedWithoutIt(t *testing.T) {
	fastLibraryWait(t)
	m := newMockBazarr(t)
	lists := 0
	m.onListTasks = func() {
		lists++
		m.tasks[0]["job_running"] = lists == 2
	}

	out, err := SyncLibrary(context.Background(), LibrarySyncRequest{RadarrID: 42})
	if err != nil {
		t.Fatal(err)
	}
	if out.Outcome != LibraryStillMissing {
		t.Fatalf("outcome %q, want %q", out.Outcome, LibraryStillMissing)
	}
	if !containsSubstring(out.Warnings, "only films that have a file") ||
		!containsSubstring(out.Warnings, "TMDB") ||
		!containsSubstring(out.Warnings, "bazarr_system_health") {
		t.Errorf("the reasons it can be missing are not given: %v", out.Warnings)
	}
}

func TestLibrarySyncGivesUpAfterTheWait(t *testing.T) {
	fastLibraryWait(t)
	newMockBazarr(t)

	out, err := SyncLibrary(context.Background(), LibrarySyncRequest{SeriesID: 7})
	if err != nil {
		t.Fatal(err)
	}
	if out.Outcome != LibraryPending || out.Source != SourceSonarr {
		t.Fatalf("got %+v", out)
	}
	if !containsSubstring(out.Warnings, "may still be running") || !containsSubstring(out.Warnings, "TVDB") {
		t.Errorf("warnings: %v", out.Warnings)
	}
}

func TestLibrarySyncLeavesARunningSyncAlone(t *testing.T) {
	m := newMockBazarr(t)
	m.tasks[1]["job_running"] = true

	out, err := SyncLibrary(context.Background(), LibrarySyncRequest{Source: "Sonarr"})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := m.sent("POST /api/system/tasks"); ok {
		t.Error("a running sync was started again")
	}
	if out.Outcome != LibraryStarted || !out.AlreadyRunning {
		t.Errorf("got %+v", out)
	}
}

func TestLibrarySyncDoesNothingForAMovieAlreadyThere(t *testing.T) {
	m := newMockBazarr(t)
	m.movies[42] = dune(1)

	out, err := SyncLibrary(context.Background(), LibrarySyncRequest{RadarrID: 42})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := m.sent("POST /api/system/tasks"); ok {
		t.Error("a sync was started for a movie Bazarr already had")
	}
	if out.Outcome != LibraryAlreadyThere || out.Movie == nil {
		t.Errorf("got %+v", out)
	}
}

func TestLibrarySyncWithoutTheTaskSaysBazarrIsNotUsingRadarr(t *testing.T) {
	m := newMockBazarr(t)
	m.tasks = m.tasks[1:]

	_, err := SyncLibrary(context.Background(), LibrarySyncRequest{Source: "radarr"})
	if err == nil || !strings.Contains(err.Error(), "Settings → Radarr") {
		t.Errorf("got %v", err)
	}
}

func TestLibrarySyncRefusesWhatItCannotDo(t *testing.T) {
	newMockBazarr(t)
	for _, req := range []LibrarySyncRequest{
		{},
		{Source: "jellyfin"},
		{RadarrID: 1, SeriesID: 2},
		{Source: "radarr", SeriesID: 2},
		{Source: "sonarr", RadarrID: 1},
		{RadarrID: -1},
	} {
		if _, err := SyncLibrary(context.Background(), req); err == nil {
			t.Errorf("%+v was accepted", req)
		}
	}
}
