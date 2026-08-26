package libbox

import (
	"math"
	"testing"
)

func TestResolveGoMemoryLimit(t *testing.T) {
	const (
		goLimit  = int64(30 * 1024 * 1024)
		oomLimit = int64(40 * 1024 * 1024)
	)

	t.Run("explicit limit is independent from OOM killer", func(t *testing.T) {
		if actual := resolveGoMemoryLimit(goLimit, false, 0); actual != goLimit {
			t.Fatalf("expected explicit Go limit %d, got %d", goLimit, actual)
		}
	})

	t.Run("explicit limit wins over legacy OOM coupling", func(t *testing.T) {
		if actual := resolveGoMemoryLimit(goLimit, true, oomLimit); actual != goLimit {
			t.Fatalf("expected explicit Go limit %d, got %d", goLimit, actual)
		}
	})

	t.Run("legacy clients retain three-quarter soft limit", func(t *testing.T) {
		expected := oomLimit * 3 / 4
		if actual := resolveGoMemoryLimit(0, true, oomLimit); actual != expected {
			t.Fatalf("expected legacy Go limit %d, got %d", expected, actual)
		}
	})

	t.Run("disabled limits restore the runtime default", func(t *testing.T) {
		if actual := resolveGoMemoryLimit(0, false, 0); actual != math.MaxInt64 {
			t.Fatalf("expected unlimited Go runtime, got %d", actual)
		}
	})
}
