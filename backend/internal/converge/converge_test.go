package converge

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestExtractKeywords(t *testing.T) {
	kw := extractKeywords(`{"story_id":"S-1","title":"hello world"}`, "my title")
	assert.Contains(t, kw, "hello")
	assert.Contains(t, kw, "world")
	assert.Contains(t, kw, "title")
}

func TestCosine(t *testing.T) {
	a := []float32{1, 0, 0}
	b := []float32{1, 0, 0}
	assert.InDelta(t, 1.0, cosine(a, b), 0.001)
	c := []float32{1, 0, 0}
	d := []float32{0, 1, 0}
	assert.InDelta(t, 0.0, cosine(c, d), 0.001)
}

func TestMathSqrt(t *testing.T) {
	assert.InDelta(t, 2.0, MathSqrt(4), 0.001)
	assert.InDelta(t, 3.0, MathSqrt(9), 0.001)
}
