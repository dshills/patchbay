package job

import "testing"

func TestTransitions(t *testing.T) {
	states := []State{Queued, Running, Success, Failed, Cancelled, "invalid"}
	allowed := map[[2]State]bool{
		{Queued, Running}: true, {Queued, Cancelled}: true,
		{Running, Success}: true, {Running, Failed}: true, {Running, Cancelled}: true,
	}
	for _, from := range states {
		for _, to := range states {
			if CanTransition(from, to) != allowed[[2]State{from, to}] {
				t.Errorf("incorrect transition %s -> %s", from, to)
			}
		}
	}
}
