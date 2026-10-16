package estimate

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestEstimate_NilDB(t *testing.T) {
	h, d, err := Estimate(context.Background(), "00000000-0000-0000-0000-000000000001", "hello", 5)
	assert.NoError(t, err)
	assert.Equal(t, 3.5, h)
	assert.Equal(t, 0.15, d)
}

func TestEstimate_PriorityFactor(t *testing.T) {
	// with nil DB, base is 4, factor applied but title length also
	h10, _, _ := Estimate(context.Background(), "x", "a", 10)
	h1, _, _ := Estimate(context.Background(), "x", "a", 1)
	assert.Greater(t, h10, h1)
}
