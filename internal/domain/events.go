package domain

import "time"

type AuditEvent struct {
	ID         int64
	DistrictID string
	ActorID    string
	RequestID  string
	ObjectType string
	ObjectID   string
	Action     string
	Result     string
	DetailJSON string
	CreatedAt  time.Time
}

type OutboxState string

const (
	OutboxPending    OutboxState = "pending"
	OutboxProcessing OutboxState = "processing"
	OutboxDelivered  OutboxState = "delivered"
	OutboxDead       OutboxState = "dead"
)

type OutboxEvent struct {
	ID            string
	DistrictID    string
	Topic         string
	AggregateType string
	AggregateID   string
	PayloadJSON   string
	State         OutboxState
	Attempts      int
	AvailableAt   time.Time
	LockedBy      string
	LockedUntil   *time.Time
	LastError     string
	CreatedAt     time.Time
	DeliveredAt   *time.Time
}

type JobState string

const (
	JobPending   JobState = "pending"
	JobRunning   JobState = "running"
	JobSucceeded JobState = "succeeded"
	JobRetry     JobState = "retry"
	JobDead      JobState = "dead"
)

type WorkerJob struct {
	ID          string
	DistrictID  string
	Kind        string
	ObjectID    string
	PayloadJSON string
	State       JobState
	Attempts    int
	MaxAttempts int
	AvailableAt time.Time
	LockedBy    string
	LockedUntil *time.Time
	LastError   string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}
