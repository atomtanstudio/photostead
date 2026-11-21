package store

import "context"

type JobCount struct {
	Queue, State string
	Count        int
}

// JobCounts summarizes River's queue (River's tables aren't part of the sqlc schema).
func (s *Store) JobCounts(ctx context.Context) ([]JobCount, error) {
	rows, err := s.Pool.Query(ctx, `SELECT queue, state::text, count(*)::int FROM river_job GROUP BY 1, 2 ORDER BY 1, 2`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []JobCount
	for rows.Next() {
		var c JobCount
		if err := rows.Scan(&c.Queue, &c.State, &c.Count); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// JobQueued says whether a job of this kind is waiting or running.
func (s *Store) JobQueued(ctx context.Context, kind string) (bool, error) {
	var queued bool
	err := s.Pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM river_job WHERE kind = $1 AND state IN ('available', 'running', 'retryable', 'scheduled'))`, kind).Scan(&queued)
	return queued, err
}
