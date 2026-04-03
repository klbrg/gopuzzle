package puzzle

import (
	"sort"

	"github.com/klbrg/gopuzzle/internal/progress"
)

// Next returns the first unsolved puzzle in ID order, or nil if all are solved.
func Next(all []*Puzzle, p *progress.Progress) *Puzzle {
	sorted := make([]*Puzzle, len(all))
	copy(sorted, all)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].ID < sorted[j].ID
	})

	for _, puz := range sorted {
		if !p.Solved[puz.ID] {
			return puz
		}
	}
	return nil
}
