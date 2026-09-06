package ranking

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

type ReadRequest struct {
	Window     string
	SnapshotID string
	Offset     int
	Limit      int
	UserID     string
}

type Entry struct {
	UserID       string
	Rank         int
	Tokens       string
	PreviousRank *int
}

type ReadResult struct {
	Kind         string
	SnapshotID   string
	Generation   string
	Revision     string
	Participants int
	Entries      []Entry
	Own          *Entry
	Miss         bool
}

func (idx *Index) Read(ctx context.Context, req ReadRequest) (*ReadResult, error) {
	if idx == nil || idx.rdb == nil {
		return &ReadResult{Miss: true}, nil
	}
	if !ValidWindow(req.Window) {
		return nil, ErrInvalidWindow
	}
	if req.Limit <= 0 {
		req.Limit = 50
	}
	if req.Offset < 0 {
		req.Offset = 0
	}
	stop := req.Offset + req.Limit - 1
	wk := keysForWindow(req.Window)
	raw, err := readScript.Run(ctx, idx.rdb, []string{wk.Current, wk.HotCurrent},
		req.SnapshotID, req.Offset, stop, req.UserID, PublicCap-1,
	).Slice()
	if err != nil {
		return nil, fmt.Errorf("ranking read lua: %w", err)
	}
	kind := scriptString(raw, 0)
	if kind == "miss" || kind == "" {
		return &ReadResult{Miss: true}, nil
	}
	participants, _ := strconv.Atoi(scriptString(raw, 4))
	result := &ReadResult{
		Kind:         kind,
		SnapshotID:   scriptString(raw, 1),
		Generation:   scriptString(raw, 2),
		Revision:     scriptString(raw, 3),
		Participants: participants,
		Entries:      make([]Entry, 0, len(raw)),
	}
	ownRank, _ := strconv.Atoi(scriptString(raw, 5))
	ownTokens := scriptString(raw, 6)
	if req.UserID != "" && ownRank > 0 {
		own := &Entry{UserID: req.UserID, Rank: ownRank, Tokens: ownTokens}
		if own.Tokens == "" {
			own.Tokens = "0"
		}
		result.Own = own
	}
	rank := req.Offset + 1
	for i := 7; i < len(raw); i++ {
		entry, err := parseHotRow(scriptString(raw, i), rank)
		if err != nil {
			return nil, err
		}
		result.Entries = append(result.Entries, entry)
		rank++
	}
	return result, nil
}

func parseHotRow(row string, rank int) (Entry, error) {
	parts := strings.SplitN(row, "\t", 3)
	if len(parts) < 2 || parts[0] == "" {
		return Entry{}, fmt.Errorf("%w: %q", ErrInvalidMember, row)
	}
	entry := Entry{
		UserID: parts[0],
		Rank:   rank,
		Tokens: parts[1],
	}
	if entry.Tokens == "" {
		entry.Tokens = "0"
	}
	if len(parts) == 3 && parts[2] != "" {
		if prev, err := strconv.Atoi(parts[2]); err == nil && prev > 0 {
			entry.PreviousRank = &prev
		}
	}
	return entry, nil
}
