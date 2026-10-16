package converge

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"forja/backend/internal/model"
	"forja/backend/internal/vector"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

type Report struct {
	TaskID     string   `json:"task_id"`
	SpecID     string   `json:"spec_id"`
	Matched    []string `json:"matched"`
	Missing    []string `json:"missing"`
	Drift      []string `json:"drift"`
	Similarity float64  `json:"similarity"`
	Converged  bool     `json:"converged"`
}

func Run(ctx context.Context, taskID string) (*Report, error) {
	if model.Pool == nil {
		return &Report{TaskID: taskID, Converged: true}, nil
	}
	// fetch task spec (spec_id may be NULL)
	var specID pgtype.UUID
	var title string
	err := model.Pool.QueryRow(ctx, `SELECT spec_id, title FROM tasks WHERE id=$1`, taskID).Scan(&specID, &title)
	if err != nil {
		return nil, fmt.Errorf("task not found: %w", err)
	}
	specIDStr := ""
	if specID.Valid {
		specIDStr = uuid.UUID(specID.Bytes).String()
	}
	// spec content
	var content json.RawMessage
	var svID string
	if specIDStr != "" {
		_ = model.Pool.QueryRow(ctx, `SELECT id, content FROM spec_versions WHERE spec_id=$1 ORDER BY created_at DESC LIMIT 1`, specIDStr).Scan(&svID, &content)
	}
	// code_changes diffs
	rows, err := model.Pool.Query(ctx, `SELECT file_path, diff FROM code_changes WHERE task_id=$1`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var diffs []string
	var files []string
	for rows.Next() {
		var fp, diff string
		_ = rows.Scan(&fp, &diff)
		diffs = append(diffs, diff)
		files = append(files, fp)
	}
	joined := strings.Join(diffs, "\n")
	rawSpec := string(content)
	// naive matching: check if spec keywords appear in diff
	// For BS, check title/spec keywords
	matched := []string{}
	missing := []string{}
	drift := []string{}
	if rawSpec != "" {
		// extract keys from spec json/yaml roughly
		keywords := extractKeywords(rawSpec, title)
		for _, kw := range keywords {
			if strings.Contains(strings.ToLower(joined), strings.ToLower(kw)) {
				matched = append(matched, kw)
			} else {
				missing = append(missing, kw)
			}
		}
		// drift: diff contains TODO/FIXME or unrelated file
		if strings.Contains(joined, "TODO") || strings.Contains(joined, "FIXME") {
			drift = append(drift, "contains TODO/FIXME")
		}
		for _, f := range files {
			if strings.Contains(f, "tmp") || strings.Contains(f, ".log") {
				drift = append(drift, "suspicious file "+f)
			}
		}
	}
	// embedding similarity if both have embeddings
	var sim float64 = 1.0
	if svID != "" && len(diffs) > 0 {
		// compare spec text vs diff text via mock embedding cosine
		sv := vector.Embed(rawSpec)
		dv := vector.Embed(joined)
		sim = cosine(sv, dv)
		if sim < 0.7 && len(matched) == 0 {
			drift = append(drift, fmt.Sprintf("low similarity %.2f", sim))
		}
	}
	converged := len(missing) == 0 && len(drift) == 0 && sim >= 0.7
	// if no spec, consider converged if no drift
	if specIDStr == "" {
		converged = len(drift) == 0
	}
	return &Report{
		TaskID:     taskID,
		SpecID:     specIDStr,
		Matched:    matched,
		Missing:    missing,
		Drift:      drift,
		Similarity: sim,
		Converged:  converged,
	}, nil
}

func extractKeywords(specRaw, title string) []string {
	// crude: split title and spec by non-alnum
	words := strings.FieldsFunc(title+ " "+specRaw, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_')
	})
	uniq := map[string]bool{}
	var out []string
	for _, w := range words {
		if len(w) < 3 {
			continue
		}
		lw := strings.ToLower(w)
		if uniq[lw] {
			continue
		}
		uniq[lw] = true
		out = append(out, w)
		if len(out) >= 10 {
			break
		}
	}
	return out
}

func cosine(a, b []float32) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		dot += float64(a[i] * b[i])
		na += float64(a[i] * a[i])
		nb += float64(b[i] * b[i])
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (MathSqrt(na) * MathSqrt(nb))
}

func MathSqrt(f float64) float64 {
	// avoid importing math for tiny
	if f < 0 {
		return 0
	}
	// use Newton
	x := f
	for i := 0; i < 10; i++ {
		x = 0.5 * (x + f/x)
	}
	return x
}
