package job

import (
	"database/sql"
	"sort"
	"strings"
)

// proseLinkChunk caps how many ids ride in one IN (…) list. A plan page
// renders every description on the board, so the candidate set is unbounded
// while SQLite's parameter limit is not.
const proseLinkChunk = 400

// ResolveProseLinks scans texts for id-shaped tokens and returns the ones
// the store recognises, mapped to the URL the inline pass should link them
// to. It is the only thing that decides whether a token in a description or
// note becomes a link: RenderProseHTML asks this map and nothing else.
//
// Two queries per call regardless of how many bodies a page renders (three
// or more only when the candidate set exceeds proseLinkChunk): one over
// tasks, one over criteria.
//
// A criterion's short id is unique per task, not per store (migration
// 0008), so the same three characters can name a criterion on two tasks.
// Such a token has no single destination and is left unresolved rather than
// linked to an arbitrary one.
func ResolveProseLinks(db *sql.DB, texts []string) (ProseLinks, error) {
	candidates := proseCandidates(texts)
	if len(candidates) == 0 {
		return ProseLinks{}, nil
	}
	links := make(ProseLinks, len(candidates))

	if err := eachChunk(candidates, func(chunk []string) error {
		rows, err := db.Query(
			`SELECT short_id FROM tasks WHERE short_id IN (`+placeholders(len(chunk))+`)`,
			asArgs(chunk)...,
		)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				return err
			}
			links[id] = "/tasks/" + id
		}
		return rows.Err()
	}); err != nil {
		return nil, err
	}

	// Criterion short ids are exactly criterionShortIDLen characters, so
	// only tokens of that width are worth a lookup — and a token already
	// resolved as a task keeps the task.
	var critCandidates []string
	for _, c := range candidates {
		if len(c) == criterionShortIDLen && links[c] == "" {
			critCandidates = append(critCandidates, c)
		}
	}
	if len(critCandidates) == 0 {
		return links, nil
	}

	owners := make(map[string]string, len(critCandidates))
	ambiguous := make(map[string]bool)
	if err := eachChunk(critCandidates, func(chunk []string) error {
		rows, err := db.Query(
			`SELECT c.short_id, t.short_id FROM task_criteria c
			 JOIN tasks t ON t.id = c.task_id
			 WHERE c.short_id IN (`+placeholders(len(chunk))+`)`,
			asArgs(chunk)...,
		)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var crit, task string
			if err := rows.Scan(&crit, &task); err != nil {
				return err
			}
			if prev, seen := owners[crit]; seen && prev != task {
				ambiguous[crit] = true
				continue
			}
			owners[crit] = task
		}
		return rows.Err()
	}); err != nil {
		return nil, err
	}
	for crit, task := range owners {
		if ambiguous[crit] {
			continue
		}
		links[crit] = "/tasks/" + task + "#crit-" + crit
	}
	return links, nil
}

// proseCandidates returns the deduplicated, sorted id-shaped tokens in
// texts: maximal runs of [A-Za-z0-9] bounded by non-alphanumerics, of a
// width a short id can have. Sorted so the queries are deterministic.
func proseCandidates(texts []string) []string {
	seen := make(map[string]bool)
	for _, text := range texts {
		start := -1
		for i := 0; i <= len(text); i++ {
			if i < len(text) && isProseIDByte(text[i]) {
				if start < 0 {
					start = i
				}
				continue
			}
			if start >= 0 {
				if n := i - start; n >= criterionShortIDLen && n <= shortIDLen {
					seen[text[start:i]] = true
				}
				start = -1
			}
		}
	}
	out := make([]string, 0, len(seen))
	for id := range seen {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

func eachChunk(ids []string, fn func([]string) error) error {
	for len(ids) > 0 {
		n := min(len(ids), proseLinkChunk)
		if err := fn(ids[:n]); err != nil {
			return err
		}
		ids = ids[n:]
	}
	return nil
}

func placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}

func asArgs(ids []string) []any {
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	return args
}
