package box

import (
	"sync"
	"testing"
)

func TestBoxBeginCloseElectsSingleOwner(t *testing.T) {
	box := &Box{}
	const callers = 64

	start := make(chan struct{})
	results := make(chan bool, callers)
	var workers sync.WaitGroup
	workers.Add(callers)
	for range callers {
		go func() {
			defer workers.Done()
			<-start
			results <- box.beginClose()
		}()
	}

	close(start)
	workers.Wait()
	close(results)

	winners := 0
	for won := range results {
		if won {
			winners++
		}
	}
	if winners != 1 {
		t.Fatalf("expected one Close owner, got %d", winners)
	}
}
