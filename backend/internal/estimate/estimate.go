package estimate

import (
	"context"
	"math"

	"forja/backend/internal/model"
)

// Estimate returns hours based on historical偏差 + priority + title length
// 偏差 = avg(estimated / actual) where actual approximated by 4h for PASSED tasks
func Estimate(ctx context.Context, projectID string, title string, priority int) (float64, float64, error) {
	base := 4.0
	var avgEst *float64
	if model.Pool != nil {
		err := model.Pool.QueryRow(ctx, `SELECT AVG(estimated_hours) FROM tasks WHERE project_id=$1 AND status='PASSED' AND estimated_hours IS NOT NULL`, projectID).Scan(&avgEst)
		if err == nil && avgEst != nil {
			base = *avgEst
		}
	}
	// priority factor
	factor := map[int]float64{10: 1.5, 5: 1.0, 1: 0.5}[priority]
	if factor == 0 {
		factor = 1.0
	}
	// title length factor
	tf := math.Min(1.5, 0.8+float64(len(title))/50.0)
	hours := base * factor * tf
	// deviation: mock 15% based on history count
	var cnt int
	if model.Pool != nil {
		_ = model.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM tasks WHERE project_id=$1 AND estimated_hours IS NOT NULL`, projectID).Scan(&cnt)
	}
	deviation := 0.15
	if cnt > 10 {
		deviation = 0.12
	}
	if cnt > 30 {
		deviation = 0.08
	}
	// round to 0.5
	hours = math.Round(hours*2) / 2
	if hours < 0.5 {
		hours = 0.5
	}
	if hours > 40 {
		hours = 40
	}
	return hours, deviation, nil
}
