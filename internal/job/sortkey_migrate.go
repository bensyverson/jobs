package job

import (
	"database/sql"
	"fmt"
)

// backfillSortKeys converts a database that still orders siblings by the
// integer sort_order columns to fractional sort keys, then drops those
// columns. It runs from OpenDB immediately after the migrator, because
// migration 0009 can only add the columns: deriving a key per row needs the
// key generator, which is Go.
//
// The presence of tasks.sort_order is the switch. Once the columns are gone
// the function is a single PRAGMA and returns. Everything happens in one
// transaction, so a failure leaves the old columns in place and the next
// open retries from the same starting point.
//
// sort_order carries no index or constraint in any migration, so SQLite's
// ALTER TABLE ... DROP COLUMN accepts it and the dead columns do not have to
// be left behind.
func backfillSortKeys(db *sql.DB) error {
	legacy, err := tableHasColumn(db, "tasks", "sort_order")
	if err != nil {
		return fmt.Errorf("backfill sort keys: %w", err)
	}
	if !legacy {
		return nil
	}

	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("backfill sort keys: %w", err)
	}
	defer tx.Rollback()

	// Tasks, grouped by parent. NULL parent (the roots) is its own group;
	// SQLite orders NULLs first, so the roots come out as one run.
	if err := assignSortKeys(tx,
		`SELECT id, COALESCE(parent_id, -1) FROM tasks ORDER BY parent_id, sort_order, id`,
		`UPDATE tasks SET sort_key = ? WHERE id = ?`,
	); err != nil {
		return fmt.Errorf("backfill task sort keys: %w", err)
	}
	if err := assignSortKeys(tx,
		`SELECT id, task_id FROM task_criteria ORDER BY task_id, sort_order, id`,
		`UPDATE task_criteria SET sort_key = ? WHERE id = ?`,
	); err != nil {
		return fmt.Errorf("backfill criterion sort keys: %w", err)
	}

	for _, stmt := range []string{
		`ALTER TABLE tasks DROP COLUMN sort_order`,
		`ALTER TABLE task_criteria DROP COLUMN sort_order`,
	} {
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("backfill sort keys: %s: %w", stmt, err)
		}
	}
	return tx.Commit()
}

// assignSortKeys walks selectQuery — which must yield (row id, group id) in
// the order the rows should end up in — and writes one increasing key per
// group through updateStmt.
func assignSortKeys(tx *sql.Tx, selectQuery, updateStmt string) error {
	rows, err := tx.Query(selectQuery)
	if err != nil {
		return err
	}
	type placement struct {
		id  int64
		key string
	}
	var placements []placement
	var group int64
	var prevKey string
	first := true
	for rows.Next() {
		var id, g int64
		if err := rows.Scan(&id, &g); err != nil {
			rows.Close()
			return err
		}
		if first || g != group {
			group, prevKey, first = g, "", false
		}
		key, err := KeyBetween(prevKey, "")
		if err != nil {
			rows.Close()
			return err
		}
		prevKey = key
		placements = append(placements, placement{id: id, key: key})
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, p := range placements {
		if _, err := tx.Exec(updateStmt, p.key, p.id); err != nil {
			return err
		}
	}
	return nil
}

func tableHasColumn(db *sql.DB, table, column string) (bool, error) {
	rows, err := db.Query("SELECT name FROM pragma_table_info(?)", table)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return false, err
		}
		if name == column {
			return true, nil
		}
	}
	return false, rows.Err()
}
