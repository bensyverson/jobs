package signals

// SubwayInput is the JSON-friendly task+block bundle accepted by
// the POST /home/graph endpoint. The dashboard's JS replay buffer
// produces a Frame at any cursor; the driver projects that frame
// into a SubwayInput and POSTs it to the server, which reuses the
// same Subway core that BuildSubway runs against the live DB. The
// reducer therefore stays in JS only — there is no parallel Go
// reducer to keep in sync.
type SubwayInput struct {
	Tasks  []SubwayInputTask  `json:"tasks"`
	Blocks []SubwayInputBlock `json:"blocks"`
}

// SubwayInputTask mirrors the fields BuildSubway pulls from the
// tasks table. ParentShortID is empty for root tasks. ClaimedBy is
// optional and only meaningful when Status == "claimed".
type SubwayInputTask struct {
	ShortID       string `json:"shortId"`
	Title         string `json:"title"`
	Status        string `json:"status"`
	ParentShortID string `json:"parentShortId,omitempty"`
	SortKey       string `json:"sortKey"`
	ClaimedBy     string `json:"claimedBy,omitempty"`
}

// SubwayInputBlock is one (blocker → blocked) edge by short ID.
type SubwayInputBlock struct {
	BlockerShortID string `json:"blockerShortId"`
	BlockedShortID string `json:"blockedShortId"`
}

// BuildSubwayFromInput is the public counterpart to BuildSubway for
// callers that already have a frame in hand. Constructs an in-memory
// graphWorld from the supplied tasks and blocks, then runs the same
// Subway core BuildSubway uses.
func BuildSubwayFromInput(in SubwayInput) Subway {
	w := worldFromInput(in)
	return buildSubway(w)
}

// worldFromInput constructs a graphWorld matching what loadGraphWorld
// would produce for a live DB carrying the same tasks and blocks.
// Mirrors loadGraphWorld's bookkeeping: child slices sorted by
// sort_key, openBlockers tracking blockers whose status is neither
// done nor canceled.
func worldFromInput(in SubwayInput) *graphWorld {
	w := &graphWorld{byID: make(map[int64]*graphTask, len(in.Tasks))}
	byShort := make(map[string]*graphTask, len(in.Tasks))
	var nextID int64 = 1
	for _, td := range in.Tasks {
		t := &graphTask{
			id:      nextID,
			shortID: td.ShortID,
			title:   td.Title,
			status:  td.Status,
			actor:   td.ClaimedBy,
			sortKey: td.SortKey,
		}
		nextID++
		w.byID[t.id] = t
		byShort[td.ShortID] = t
	}
	for _, td := range in.Tasks {
		t := byShort[td.ShortID]
		if td.ParentShortID == "" {
			w.roots = append(w.roots, t)
			continue
		}
		p, ok := byShort[td.ParentShortID]
		if !ok {
			// Orphan: parent referenced but not supplied. Treat as
			// root rather than dropping the task — matches the
			// defensive posture of loadGraphWorld, which silently
			// skips dangling parent_id values.
			w.roots = append(w.roots, t)
			continue
		}
		t.parent = p
		pid := p.id
		t.parentID = &pid
		p.children = append(p.children, t)
	}
	sortBySortKey(w.roots)
	for _, t := range w.byID {
		sortBySortKey(t.children)
	}
	for _, b := range in.Blocks {
		blocker, okB := byShort[b.BlockerShortID]
		blocked, okT := byShort[b.BlockedShortID]
		if !okB || !okT {
			continue
		}
		blocked.blockerIDs = append(blocked.blockerIDs, blocker.id)
		if blocker.status != "done" && blocker.status != "canceled" {
			blocked.openBlockers++
		}
	}
	return w
}
