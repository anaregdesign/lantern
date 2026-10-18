package decisiontrace

// ContextState is a fixed diagnostic classification, never a time input.
type ContextState string

const (
	ContextUnobserved       ContextState = "unobserved"
	ContextLive             ContextState = "live"
	ContextCanceled         ContextState = "canceled"
	ContextDeadlineExceeded ContextState = "deadline_exceeded"
	ContextOtherError       ContextState = "other_error"
)

// StartupAttempt contains only copied classifications from one completed
// exchange. RemainingNS is observed at Read entry, before cleanup cancellation.
// It contains no endpoint, packet, identity, UTC or native counter sample.
type StartupAttempt struct {
	Attempt         uint64
	Stage           string
	Reason          string
	HasReadBudget   bool
	ReadRemainingNS int64
	ExchangeContext ContextState
	IncomingContext ContextState
}

// StartupSnapshot is a fixed-size value returned with a failed constructor.
// Attempts are accepted owner observations in chronological order. Producer
// state is separate: a completed but unpublished or in-flight exchange must
// not inherit the last accepted attempt's error or classification.
type StartupSnapshot struct {
	Attempts         [4]StartupAttempt
	Count            uint8
	Overflow         bool
	PublishedAttempt uint64
	StartedAttempt   uint64
	CompletedAttempt uint64
	InFlight         bool
	OwnerFailed      bool
	OwnerClosed      bool
	RemainingNS      int64
	AnchoringContext ContextState
	IncomingContext  ContextState
	OwnerContext     ContextState
}
