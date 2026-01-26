package merge

import (
	"sync"
	"testing"
	"time"
)

func TestEngineConcurrentAccess(t *testing.T) {
	e := NewEngine[int]()
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		for range 100 {
			e.Register("key", func(old, new int) (int, error) {
				return new, nil
			})
		}
	}()

	go func() {
		defer wg.Done()
		for i := range 100 {
			_, err := e.Merge("key", Value[int]{Data: i, Timestamp: time.Now()}, Value[int]{Data: i + 1, Timestamp: time.Now()})
			if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		}
	}()

	wg.Wait()
}
