package outbox

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/11DingKing/silvercare-coordination/internal/domain"
	"github.com/11DingKing/silvercare-coordination/internal/idgen"
	"github.com/11DingKing/silvercare-coordination/internal/store"
)

type Repository interface {
	EnqueueOutbox(context.Context, store.DBTX, domain.OutboxEvent) error
}

type Publisher struct {
	repo Repository
	ids  idgen.Generator
}

func NewPublisher(repo Repository, ids idgen.Generator) *Publisher {
	return &Publisher{repo: repo, ids: ids}
}

func (p *Publisher) Enqueue(ctx context.Context, q store.DBTX, districtID, topic, aggregateType, aggregateID string, payload any, now time.Time) error {
	if districtID == "" || topic == "" || aggregateType == "" || aggregateID == "" {
		return fmt.Errorf("outbox event identity is incomplete")
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode outbox payload: %w", err)
	}
	id, err := p.ids.New("event")
	if err != nil {
		return fmt.Errorf("generate outbox id: %w", err)
	}
	return p.repo.EnqueueOutbox(ctx, q, domain.OutboxEvent{
		ID: id, DistrictID: districtID, Topic: topic, AggregateType: aggregateType,
		AggregateID: aggregateID, PayloadJSON: string(raw), State: domain.OutboxPending,
		AvailableAt: now.UTC(), CreatedAt: now.UTC(),
	})
}
