package concurrentaudittail_test

import (
	"context"
	"encoding/json"
	"testing"

	"drill-seal-handover/internal/audit"
	"drill-seal-handover/internal/domain"
	"drill-seal-handover/internal/store"
)

type marshalBarrier struct {
	arrived chan<- struct{}
	release <-chan struct{}
	value   string
}

func (p marshalBarrier) MarshalJSON() ([]byte, error) {
	p.arrived <- struct{}{}
	<-p.release
	return json.Marshal(map[string]string{"value": p.value})
}

func TestConcurrentAuditAppendUsesAtomicTail(t *testing.T) {
	repository, err := store.Open("file:concurrent-audit-tail?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repository.Close() })

	chains := []*audit.Chain{audit.New(repository), audit.New(repository)}
	actor := domain.Actor{Name: "复核员", Role: domain.RoleReviewer}
	ready := make(chan struct{}, len(chains))
	start := make(chan struct{})
	arrived := make(chan struct{}, len(chains))
	release := make(chan struct{})
	results := make(chan error, len(chains))

	for index, chain := range chains {
		go func(index int, chain *audit.Chain) {
			ready <- struct{}{}
			<-start
			payload := marshalBarrier{arrived: arrived, release: release, value: string(rune('A' + index))}
			results <- chain.Append(context.Background(), "task-1", actor, "review.completed", int64(index+1), "", payload)
		}(index, chain)
	}
	for range chains {
		<-ready
	}
	close(start)
	for range chains {
		<-arrived
	}
	close(release)
	for range chains {
		if err := <-results; err != nil {
			t.Fatalf("append audit event: %v", err)
		}
	}

	if err := audit.New(repository).Verify(context.Background()); err != nil {
		t.Fatalf("concurrent audit appends must form one chain: %v", err)
	}
}
