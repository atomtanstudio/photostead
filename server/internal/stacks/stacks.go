// Package stacks finds items that belong together beyond RAW+JPEG pairs (internal/jobs pairing):
//
//   - AI batches: images (or clips) made from one prompt with one model, each within a few minutes
//     of the last. Stacked on their own, the favorite or best-rated on top.
//   - Bursts: a camera's run of shots a second or less apart (or sharing the burst identifier the
//     camera wrote), three or more. Stacked on their own.
//   - Near-duplicates: real photos that look all but the same (by their search embeddings), such as
//     the same picture saved twice, and videos that do and run as long. Never blank pictures (black
//     frames, plain title cards), which all look alike. Only suggested: the owner says whether to
//     stack them.
//
// What the owner takes out of a stack, or keeps apart, is never put back (stack_exclusions).
// See docs/architecture.md, "Organizing".
package stacks

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"photostead.com/server/internal/media"
	"photostead.com/server/internal/search"
	"photostead.com/server/internal/store"
	"photostead.com/server/internal/store/db"
)

const (
	BatchGap   = 3 * time.Minute         // the longest wait between two images of one AI batch
	BurstGap   = 1100 * time.Millisecond // between two shots of a burst
	BurstMin   = 3                       // shots that make a burst
	LookAlike  = 0.99                    // search-embedding similarity for near-duplicates; at 0.97, similar but different shots came in
	neighbours = 8
	maxGroup   = 50
	// Two videos are look-alikes only if they run as long, give or take this or 1%: different videos
	// often open on the same frame (a channel's intro, a title card), and at a second, 4- and 5-second
	// clips chained together.
	videoSlackMs = 500
)

// Stats says what a pass did.
type Stats struct {
	Batches, BatchItems int // AI batch stacks made, and items put in batch stacks
	Bursts, BurstItems  int
	Looks, LookItems    int // suggested look-alike groups made or grown, and items in them
	Checked             int // items compared for look-alikes
}

// member is an item that may go into a stack of one kind.
type member struct {
	id, owner uuid.UUID
	stack     uuid.NullUUID // the stack it's in, of this kind
	free      bool          // in no stack and never taken out of one of this kind
	score     int           // favorite and stars, for the top
}

// place puts a run of items into a stack of a kind: the run's existing stack grows, or a new one is
// made when enough of them are free.
func place(ctx context.Context, q *db.Queries, kind, state string, run []member, least int) (made bool, added int, err error) {
	var stack uuid.NullUUID
	var free []uuid.UUID
	best := -1
	for i, m := range run {
		if m.stack.Valid && !stack.Valid {
			stack = m.stack
		}
		if m.free {
			free = append(free, m.id)
			if best < 0 || m.score > run[best].score {
				best = i
			}
		}
	}
	switch {
	case stack.Valid && len(free) > 0:
		return false, len(free), q.AddStackMembers(ctx, db.AddStackMembersParams{StackID: stack.UUID, AssetIds: free})
	case !stack.Valid && len(free) >= least:
		id, err := q.NewStack(ctx, db.NewStackParams{OwnerID: run[best].owner, Kind: kind, Cover: run[best].id, State: state})
		if err != nil {
			return false, 0, err
		}
		return true, len(free), q.AddStackMembers(ctx, db.AddStackMembersParams{StackID: id, AssetIds: free})
	}
	return false, 0, nil
}

// Run stacks AI batches and bursts for everyone.
func Run(ctx context.Context, s *store.Store, log *slog.Logger) (Stats, error) {
	var st Stats
	rows, err := s.Q.AIBatchCandidates(ctx)
	if err != nil {
		return st, err
	}
	var run []member
	flush := func() error {
		if len(run) >= 2 {
			made, n, err := place(ctx, s.Q, "ai_batch", "confirmed", run, 2)
			if err != nil {
				return err
			}
			if made {
				st.Batches++
			}
			st.BatchItems += n
		}
		run = run[:0]
		return nil
	}
	for i, r := range rows {
		if i > 0 {
			p := rows[i-1]
			if p.OwnerID != r.OwnerID || p.Type != r.Type || p.PromptID != r.PromptID || p.ModelID != r.ModelID || r.TakenAt.Sub(p.TakenAt) > BatchGap {
				if err := flush(); err != nil {
					return st, err
				}
			}
		}
		m := member{id: r.ID, owner: r.OwnerID, score: int(r.Rating)}
		if r.Favorite {
			m.score += 10
		}
		switch {
		case r.StackID.Valid && r.StackKind.String == "ai_batch":
			m.stack = r.StackID
		case !r.StackID.Valid && !r.Excluded:
			m.free = true
		}
		run = append(run, m)
	}
	if err := flush(); err != nil {
		return st, err
	}

	bursts, err := s.Q.BurstCandidates(ctx)
	if err != nil {
		return st, err
	}
	run = run[:0]
	flushBurst := func() error {
		if len(run) >= BurstMin {
			made, n, err := place(ctx, s.Q, "burst", "confirmed", run, BurstMin)
			if err != nil {
				return err
			}
			if made {
				st.Bursts++
			}
			st.BurstItems += n
		}
		run = run[:0]
		return nil
	}
	for i, r := range bursts {
		if i > 0 {
			p := bursts[i-1]
			same := p.OwnerID == r.OwnerID && p.Make == r.Make && p.Model == r.Model
			together := (r.Burst != "" && r.Burst == p.Burst) || (r.Burst == "" && p.Burst == "" && r.TakenAt.Sub(p.TakenAt) <= BurstGap)
			if !same || !together {
				if err := flushBurst(); err != nil {
					return st, err
				}
			}
		}
		m := member{id: r.ID, owner: r.OwnerID}
		switch {
		case r.StackID.Valid && r.StackKind.String == "burst":
			m.stack = r.StackID
		case !r.StackID.Valid && !r.Excluded:
			m.free = true
		}
		run = append(run, m)
	}
	if err := flushBurst(); err != nil {
		return st, err
	}
	if log != nil && st.BatchItems+st.BurstItems > 0 {
		log.Info("stacked", "AI batches", st.Batches, "batch items", st.BatchItems, "bursts", st.Bursts, "burst items", st.BurstItems)
	}
	return st, nil
}

// look is what decides whether two items that look alike may be look-alikes.
type look struct {
	video    bool
	duration int32 // milliseconds
	blank    bool
}

func lookOf(typ string, durationMs int32, thumbhash []byte) look {
	spread, ok := media.HashSpread(thumbhash)
	return look{video: typ == "video", duration: durationMs, blank: ok && spread < media.FlatSpread}
}

// alike says whether two items whose pictures are 0.99 alike are look-alikes: neither blank, both
// photos, or both videos of the same length.
func alike(a, b look) bool {
	if a.blank || b.blank || a.video != b.video {
		return false
	}
	if !a.video {
		return true
	}
	if a.duration <= 0 || b.duration <= 0 {
		return false
	}
	d, long := a.duration-b.duration, max(a.duration, b.duration)
	if d < 0 {
		d = -d
	}
	return d <= videoSlackMs || float64(d) <= 0.01*float64(long)
}

// neighbourSQL finds an embedding's nearest items through the HNSW index (the model's partial
// index needs the model named as is).
var neighbourSQL = fmt.Sprintf(`
SELECT e.asset_id, 1 - ((e.embedding::halfvec(768)) <=> $1::halfvec(768)) AS sim
FROM asset_embeddings e WHERE e.model = '%s'
ORDER BY (e.embedding::halfvec(768)) <=> $1::halfvec(768)
LIMIT %d`, search.Model, neighbours+1)

// NearDuplicates compares up to limit items not compared before with their nearest neighbours,
// and suggests look-alike groups (new, or grown).
func NearDuplicates(ctx context.Context, s *store.Store, limit int, log *slog.Logger) (Stats, error) {
	var st Stats
	cands, err := s.Q.NearDuplicateCandidates(ctx, db.NearDuplicateCandidatesParams{Model: search.Model, Lim: int32(limit)})
	if err != nil || len(cands) == 0 {
		return st, err
	}
	// Union-find over the items and the look-alikes they reach (see alike).
	parent := map[uuid.UUID]uuid.UUID{}
	var find func(uuid.UUID) uuid.UUID
	find = func(x uuid.UUID) uuid.UUID {
		p, ok := parent[x]
		if !ok || p == x {
			parent[x] = x
			return x
		}
		r := find(p)
		parent[x] = r
		return r
	}
	var checked []uuid.UUID
	type edge struct{ a, b uuid.UUID }
	var edges []edge
	err = pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "SET LOCAL hnsw.ef_search = 64"); err != nil {
			return err
		}
		for _, c := range cands {
			checked = append(checked, c.ID)
			if lookOf("", 0, c.Thumbhash).blank {
				continue // a blank picture is like every other blank one
			}
			rows, err := tx.Query(ctx, neighbourSQL, c.Vec)
			if err != nil {
				return err
			}
			for rows.Next() {
				var id uuid.UUID
				var sim float64
				if err := rows.Scan(&id, &sim); err != nil {
					rows.Close()
					return err
				}
				if id != c.ID && sim >= LookAlike {
					edges = append(edges, edge{c.ID, id})
				}
			}
			rows.Close()
			if err := rows.Err(); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return st, err
	}
	// Which of the items reached may be grouped: the owner's real ones, in no stack (or in a
	// suggested look-alike group), never kept apart.
	seen := map[uuid.UUID]bool{}
	for _, e := range edges {
		seen[e.a], seen[e.b] = true, true
	}
	all := make([]uuid.UUID, 0, len(seen))
	for id := range seen {
		all = append(all, id)
	}
	ok := map[uuid.UUID]db.NearDuplicatePeersRow{}
	for i := 0; i < len(all); i += 5000 {
		peers, err := s.Q.NearDuplicatePeers(ctx, all[i:min(i+5000, len(all))])
		if err != nil {
			return st, err
		}
		for _, p := range peers {
			if !p.StackID.Valid || (p.StackKind.String == "near_duplicate" && p.StackState.String == "suggested") {
				ok[p.ID] = p
			}
		}
	}
	for _, e := range edges {
		a, aok := ok[e.a]
		b, bok := ok[e.b]
		if aok && bok && alike(lookOf(a.Type, a.DurationMs, a.Thumbhash), lookOf(b.Type, b.DurationMs, b.Thumbhash)) {
			parent[find(e.b)] = find(e.a)
		}
	}
	groups := map[uuid.UUID][]db.NearDuplicatePeersRow{}
	for _, id := range all {
		if p, good := ok[id]; good {
			r := find(id)
			groups[r] = append(groups[r], p)
		}
	}
	for _, g := range groups {
		if len(g) < 2 || len(g) > maxGroup {
			continue
		}
		run := make([]member, 0, len(g))
		for _, p := range g {
			if p.OwnerID != g[0].OwnerID {
				continue
			}
			m := member{id: p.ID, owner: p.OwnerID, score: int(min(p.Pixels/1000, 1<<30))} // the largest on top
			if p.StackID.Valid {
				m.stack = p.StackID
			} else {
				m.free = true
			}
			run = append(run, m)
		}
		if len(run) < 2 {
			continue
		}
		made, n, err := place(ctx, s.Q, "near_duplicate", "suggested", run, 2)
		if err != nil {
			return st, err
		}
		if made || n > 0 {
			st.Looks++
		}
		st.LookItems += n
	}
	for i := 0; i < len(checked); i += 5000 {
		if err := s.Q.MarkNearDuplicateChecked(ctx, checked[i:min(i+5000, len(checked))]); err != nil {
			return st, err
		}
	}
	st.Checked = len(checked)
	if log != nil {
		log.Info("look-alikes compared", "items", st.Checked, "groups suggested or grown", st.Looks, "items in them", st.LookItems)
	}
	return st, nil
}
