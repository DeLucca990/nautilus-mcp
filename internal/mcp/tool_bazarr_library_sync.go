package mcp

import (
	"context"
	"fmt"
	"strings"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/DeLucca990/nautilus/internal/bazarr"
)

type bazarrLibrarySyncInput struct {
	Source   string `json:"source,omitempty" jsonschema:"the library to copy: radarr or sonarr. Implied by radarr_id or series_id"`
	RadarrID int    `json:"radarr_id,omitempty" jsonschema:"the movie Bazarr is missing, by Radarr's own id — the 'id' radarr_library_status returns, not the TMDB id. The call waits until it arrives"`
	SeriesID int    `json:"series_id,omitempty" jsonschema:"the series Bazarr is missing, by Sonarr's own id — not the TVDB id. The call waits until it arrives"`
}

func handleBazarrLibrarySync(
	ctx context.Context,
	req *sdk.CallToolRequest,
	in bazarrLibrarySyncInput,
) (*sdk.CallToolResult, bazarr.LibrarySync, error) {
	out, err := bazarr.SyncLibrary(ctx, bazarr.LibrarySyncRequest{
		Source:   in.Source,
		RadarrID: in.RadarrID,
		SeriesID: in.SeriesID,
	})
	if err != nil {
		return nil, bazarr.LibrarySync{}, err
	}
	return &sdk.CallToolResult{
		Content: []sdk.Content{
			&sdk.TextContent{Text: renderBazarrLibrarySync(out)},
		},
	}, out, nil
}

func renderBazarrLibrarySync(r bazarr.LibrarySync) string {
	var b strings.Builder

	item := bazarrLibraryItem(r)
	switch r.Outcome {
	case bazarr.LibraryAlreadyThere:
		fmt.Fprintf(&b, "%s was already in Bazarr, so no sync was started\n", item)
	case bazarr.LibraryArrived:
		fmt.Fprintf(&b, "%s is in Bazarr now, after bazarr's %s (%ds)\n", item, r.Task, r.WaitedSeconds)
	case bazarr.LibraryStillMissing:
		fmt.Fprintf(&b, "bazarr's %s finished and %s is still not there\n", r.Task, item)
	case bazarr.LibraryPending:
		fmt.Fprintf(&b, "%s is not in Bazarr yet after %ds of bazarr's %s\n", item, r.WaitedSeconds, r.Task)
	default:
		if r.AlreadyRunning {
			fmt.Fprintf(&b, "bazarr's %s was already running, and was left to finish\n", r.Task)
		} else {
			fmt.Fprintf(&b, "bazarr started its %s, which runs in the background\n", r.Task)
		}
	}
	if r.Interval != "" && r.Interval != "None" {
		fmt.Fprintf(&b, "on its own, bazarr runs it %s\n", r.Interval)
	}

	switch {
	case r.Movie != nil:
		m := r.Movie
		fmt.Fprintf(&b, "\nlanguage profile: %s\n", blankAs(m.ProfileName, profileIDCell(m.ProfileID)))
		writeSubtitleBlock(&b, m.AudioLanguages, m.Subtitles, m.Missing, m.SceneName)
	case r.Series != nil:
		s := r.Series
		fmt.Fprintf(&b, "\nlanguage profile: %s\n", blankAs(s.ProfileName, profileIDCell(s.ProfileID)))
		fmt.Fprintf(&b, "%d episode file(s), %d lacking a subtitle the profile asks for\n",
			s.EpisodeFileCount, s.EpisodesMissingSubtitles)
	}

	writeWarnings(&b, r.Warnings)
	return b.String()
}

func bazarrLibraryItem(r bazarr.LibrarySync) string {
	switch {
	case r.Movie != nil:
		return fmt.Sprintf("%s [radarr_id %d]", movieTitle(r.Movie.Title, r.Movie.Year), r.Movie.RadarrID)
	case r.Series != nil:
		return fmt.Sprintf("%s [series_id %d]", movieTitle(r.Series.Title, r.Series.Year), r.Series.SeriesID)
	case r.RadarrID > 0:
		return fmt.Sprintf("radarr_id %d", r.RadarrID)
	default:
		return fmt.Sprintf("series_id %d", r.SeriesID)
	}
}
