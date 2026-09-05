package files

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseRangeHeader(t *testing.T) {
	t.Run("empty header returns full content", func(t *testing.T) {
		rangeValue, hasRange, err := ParseRangeHeader("", 100)
		require.NoError(t, err)
		require.False(t, hasRange)
		require.Zero(t, rangeValue.Start)
	})

	t.Run("closed range", func(t *testing.T) {
		rangeValue, hasRange, err := ParseRangeHeader("bytes=0-9", 100)
		require.NoError(t, err)
		require.True(t, hasRange)
		require.Equal(t, int64(0), rangeValue.Start)
		require.Equal(t, int64(9), rangeValue.End)
	})

	t.Run("open ended range", func(t *testing.T) {
		rangeValue, hasRange, err := ParseRangeHeader("bytes=50-", 100)
		require.NoError(t, err)
		require.True(t, hasRange)
		require.Equal(t, int64(50), rangeValue.Start)
		require.Equal(t, int64(99), rangeValue.End)
	})

	t.Run("suffix range", func(t *testing.T) {
		rangeValue, hasRange, err := ParseRangeHeader("bytes=-10", 100)
		require.NoError(t, err)
		require.True(t, hasRange)
		require.Equal(t, int64(90), rangeValue.Start)
		require.Equal(t, int64(99), rangeValue.End)
	})

	t.Run("invalid range", func(t *testing.T) {
		_, _, err := ParseRangeHeader("bytes=200-300", 100)
		require.ErrorIs(t, err, ErrRangeNotSatisfiable)
	})
}
