package api

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"photostead.com/server/internal/auth"
	"photostead.com/server/internal/store/db"
)

// Memories are made when asked for, from the viewer's items: "on this day" in each earlier year,
// and one named person (or pet) over the years, a different one each day. Trips wait for places.

const (
	memoryItems = 24 // the most a memory shows
	weekAround  = 3  // days either side, when nothing happened on the day itself
)

// Memory is one card in the strip above the timeline.
type Memory struct {
	Key      string          `json:"key" doc:"Stays the same for this memory today"`
	Kind     string          `json:"kind" enum:"on_this_day,person"`
	Title    string          `json:"title"`
	Subtitle string          `json:"subtitle"`
	From     string          `json:"from" doc:"The first day it remembers (to hide it)"`
	To       string          `json:"to" doc:"The last day"`
	Person   *Person         `json:"person,omitempty" doc:"Who a person's memory is about"`
	Count    int32           `json:"count" doc:"Items it could show; the best come in assets"`
	Assets   []TimelineAsset `json:"assets"`
}

type memoriesInput struct {
	Date string `query:"date" pattern:"^([0-9]{4}-[0-9]{2}-[0-9]{2})?$" doc:"The day to remember from (default: today, in your time zone)"`
}

type memoriesOutput struct {
	Body struct {
		Date     string   `json:"date"`
		Memories []Memory `json:"memories"`
	}
}

// HiddenDays is a stretch of time kept out of memories.
type HiddenDays struct {
	ID   uuid.UUID `json:"id"`
	From string    `json:"from"`
	To   string    `json:"to"`
}

type memorySettingsOutput struct {
	Body struct {
		HiddenDays   []HiddenDays `json:"hiddenDays" doc:"Newest first"`
		HiddenPeople []Person     `json:"hiddenPeople" doc:"People and pets kept out of memories"`
	}
}

type hideDaysInput struct {
	Body struct {
		From string `json:"from" pattern:"^[0-9]{4}-[0-9]{2}-[0-9]{2}$"`
		To   string `json:"to,omitempty" pattern:"^([0-9]{4}-[0-9]{2}-[0-9]{2})?$" doc:"The same as from when left out"`
	}
}

type hiddenDaysOutput struct{ Body HiddenDays }

// memoryRow is an item of a memory (PersonMemory's row; OnThisDay's has its day's count too).
type memoryRow struct {
	ID           uuid.UUID
	Type         string
	TakenAt      time.Time
	LocalDate    pgtype.Date
	Width        pgtype.Int4
	Height       pgtype.Int4
	DurationMs   pgtype.Int4
	Thumbhash    []byte
	RenderState  string
	LiveMotionID uuid.NullUUID
	SourceType   string
	Favorite     bool
	Rating       pgtype.Int2
	Flag         pgtype.Text
	ColorLabel   pgtype.Text
	Rank         int32
}

func dayRow(r db.OnThisDayRow) memoryRow {
	return memoryRow{r.ID, r.Type, r.TakenAt, r.LocalDate, r.Width, r.Height, r.DurationMs, r.Thumbhash, r.RenderState,
		r.LiveMotionID, r.SourceType, r.Favorite, r.Rating, r.Flag, r.ColorLabel, r.Rank}
}

func (r memoryRow) asset() TimelineAsset {
	a := TimelineAsset{ID: r.ID, Type: r.Type, TakenAt: r.TakenAt, LocalDate: day(r.LocalDate), Width: r.Width.Int32, Height: r.Height.Int32,
		DurationMs: r.DurationMs.Int32, ThumbHash: r.Thumbhash, Render: r.RenderState, Favorite: r.Favorite, AI: aiKind(r.SourceType)}
	a.marks(r.Rating, r.Flag, r.ColorLabel)
	if r.LiveMotionID.Valid {
		id := r.LiveMotionID.UUID
		a.MotionID = &id
	}
	return a
}

// sameDay is a date's month and day in another year (February 29th is the 28th in other years).
func sameDay(d time.Time, year int) time.Time {
	m, dd := d.Month(), d.Day()
	if m == time.February && dd == 29 && time.Date(year, 3, 0, 0, 0, 0, 0, time.UTC).Day() != 29 {
		dd = 28
	}
	return time.Date(year, m, dd, 0, 0, 0, 0, time.UTC)
}

func yearsAgo(n int) string {
	if n == 1 {
		return "A year ago"
	}
	return fmt.Sprintf("%d years ago", n)
}

func (s *server) onThisDay(ctx context.Context, today time.Time, includeAI bool, seed string) ([]Memory, error) {
	for _, around := range []int{0, weekAround} { // the day itself, else the week around it
		var days []time.Time
		target := map[string]int{} // a day asked for -> the year it stands for
		for y := today.Year() - 1; y >= 1900; y-- {
			d := sameDay(today, y)
			for k := -around; k <= around; k++ {
				dk := d.AddDate(0, 0, k)
				days = append(days, dk)
				target[dk.Format("2006-01-02")] = y
			}
		}
		pd := make([]pgtype.Date, len(days))
		for i, d := range days {
			pd[i] = pgtype.Date{Time: d, Valid: true}
		}
		rows, err := s.Store.Q.OnThisDay(ctx, db.OnThisDayParams{Viewer: viewer(ctx), Days: pd, IncludeAi: includeAI, Seed: seed, PerDay: memoryItems})
		if err != nil {
			return nil, err
		}
		if len(rows) == 0 {
			continue
		}
		var out []Memory
		byYear := map[int]int{} // year -> index in out
		for _, r := range rows {
			y := target[day(r.LocalDate)]
			i, ok := byYear[y]
			if !ok {
				d := sameDay(today, y)
				m := Memory{Key: fmt.Sprintf("day:%d-%s", y, today.Format("01-02")), Kind: "on_this_day", Title: yearsAgo(today.Year() - y),
					Subtitle: d.Format("January 2, 2006"), From: d.AddDate(0, 0, -around).Format("2006-01-02"), To: d.AddDate(0, 0, around).Format("2006-01-02")}
				if around > 0 {
					m.Subtitle = "The week of " + m.Subtitle
				}
				i = len(out)
				byYear[y] = i
				out = append(out, m)
			}
			out[i].Count += r.OnDay * btoi(r.Rank == 1) // each day's total, once
			if len(out[i].Assets) < memoryItems {
				out[i].Assets = append(out[i].Assets, dayRow(r).asset())
			}
		}
		return out, nil
	}
	return nil, nil
}

func btoi(b bool) int32 {
	if b {
		return 1
	}
	return 0
}

// personMemory is one named person (or pet) over the years, a different one each day.
func (s *server) personMemory(ctx context.Context, today time.Time, includeAI bool, seed string) (*Memory, error) {
	people, err := s.Store.Q.MemoryPeople(ctx, db.MemoryPeopleParams{Viewer: viewer(ctx), IncludeAi: includeAI})
	if err != nil || len(people) == 0 {
		return nil, err
	}
	p := people[today.YearDay()%len(people)]
	rows, err := s.Store.Q.PersonMemory(ctx, db.PersonMemoryParams{Viewer: viewer(ctx), PersonID: p.ID, IncludeAi: includeAI, Seed: seed})
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	// Spread over the whole time, oldest first: every nth when there are more than a memory holds.
	assets := make([]TimelineAsset, 0, memoryItems)
	for i := 0; i < memoryItems && i < len(rows); i++ {
		assets = append(assets, memoryRow(rows[i*len(rows)/min(len(rows), memoryItems)]).asset())
	}
	first, last := p.FirstDay.Time, p.LastDay.Time
	m := &Memory{Key: "person:" + p.ID.String(), Kind: "person", Count: p.Items, Assets: assets,
		From: day(p.FirstDay), To: day(p.LastDay), Person: &Person{ID: p.ID, Name: p.Name, Species: p.Species.String, Kind: map[bool]string{true: "pet", false: "human"}[p.Species.Valid]}}
	if first.Year() == last.Year() {
		m.Title, m.Subtitle = "With "+p.Name, first.Format("January")+" – "+last.Format("January 2006")
	} else {
		m.Title, m.Subtitle = p.Name+" over the years", fmt.Sprintf("%d – %d", first.Year(), last.Year())
	}
	return m, nil
}

func (s *server) registerMemories() {
	huma.Register(s.api, huma.Operation{
		OperationID: "memories", Method: http.MethodGet, Path: "/api/v1/memories", Tags: []string{"Memories"},
		Summary: "Today's memories",
		Description: "On this day in each earlier year (or the week around it, when the day itself has nothing), and one named person or pet over the years. " +
			"Never screenshots, documents and the like, rejects, the vault, stretches of time or people you keep out, or (unless your settings say so) AI-made images.",
		Security: sessionAuth,
	}, func(ctx context.Context, in *memoriesInput) (*memoriesOutput, error) {
		u, err := s.Store.Q.GetUser(ctx, viewer(ctx))
		if err != nil {
			return nil, err
		}
		set := s.userOf(u).Settings
		today := time.Now()
		if loc, err := time.LoadLocation(auth.EffectiveZone(u, s.ServerZone)); err == nil {
			today = today.In(loc)
		}
		today = time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, time.UTC)
		if in.Date != "" {
			if today, err = time.Parse("2006-01-02", in.Date); err != nil {
				return nil, huma.Error422UnprocessableEntity("date isn't a day")
			}
		}
		seed := today.Format("2006-01-02")
		out := &memoriesOutput{}
		out.Body.Date = seed
		if out.Body.Memories, err = s.onThisDay(ctx, today, set.MemoriesAI, seed); err != nil {
			return nil, err
		}
		pm, err := s.personMemory(ctx, today, set.MemoriesAI, seed)
		if err != nil {
			return nil, err
		}
		if pm != nil {
			out.Body.Memories = append(out.Body.Memories, *pm)
		}
		if out.Body.Memories == nil {
			out.Body.Memories = []Memory{}
		}
		return out, nil
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "memorySettings", Method: http.MethodGet, Path: "/api/v1/memories/settings", Tags: []string{"Memories"},
		Summary: "The days and people you keep out of memories", Security: sessionAuth,
	}, func(ctx context.Context, _ *struct{}) (*memorySettingsOutput, error) {
		days, err := s.Store.Q.MemoryHiddenDays(ctx, viewer(ctx))
		if err != nil {
			return nil, err
		}
		people, err := s.Store.Q.PeopleKeptOutOfMemories(ctx, viewer(ctx))
		if err != nil {
			return nil, err
		}
		out := &memorySettingsOutput{}
		out.Body.HiddenDays = make([]HiddenDays, len(days))
		for i, d := range days {
			out.Body.HiddenDays[i] = HiddenDays{ID: d.ID, From: day(d.FromDate), To: day(d.ToDate)}
		}
		out.Body.HiddenPeople = make([]Person, len(people))
		for i, p := range people {
			out.Body.HiddenPeople[i] = Person{ID: p.ID, Name: p.Name, Species: p.Species.String, Kind: map[bool]string{true: "pet", false: "human"}[p.Species.Valid], KeptOutOfMemories: true}
		}
		return out, nil
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "hideMemoryDays", Method: http.MethodPost, Path: "/api/v1/memories/hidden", Tags: []string{"Memories"},
		Summary: "Keep a day, or a stretch of days, out of memories", Security: sessionAuth, DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, in *hideDaysInput) (*hiddenDaysOutput, error) {
		from, err := time.Parse("2006-01-02", in.Body.From)
		if err != nil {
			return nil, huma.Error422UnprocessableEntity("from isn't a day")
		}
		to := from
		if in.Body.To != "" {
			if to, err = time.Parse("2006-01-02", in.Body.To); err != nil || to.Before(from) {
				return nil, huma.Error422UnprocessableEntity("to must be a day, no earlier than from")
			}
		}
		id, err := s.Store.Q.HideMemoryDays(ctx, db.HideMemoryDaysParams{Viewer: viewer(ctx), FromDate: pgtype.Date{Time: from, Valid: true}, ToDate: pgtype.Date{Time: to, Valid: true}})
		if err != nil {
			return nil, err
		}
		return &hiddenDaysOutput{Body: HiddenDays{ID: id, From: from.Format("2006-01-02"), To: to.Format("2006-01-02")}}, nil
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "unhideMemoryDays", Method: http.MethodDelete, Path: "/api/v1/memories/hidden/{id}", Tags: []string{"Memories"},
		Summary: "Let a hidden stretch of days back into memories", Security: sessionAuth, DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, in *idInput) (*struct{}, error) {
		n, err := s.Store.Q.UnhideMemoryDays(ctx, db.UnhideMemoryDaysParams{ID: in.ID, Viewer: viewer(ctx)})
		if err != nil {
			return nil, err
		}
		if n == 0 {
			return nil, huma.Error404NotFound("No such hidden days.")
		}
		return nil, nil
	})
}
