package security

const (
	DefaultSystemJournalMax = int64(64 << 20)
	MaxSystemJournalBytes   = int64(512 << 20)
	retainedChanges         = 256
)
