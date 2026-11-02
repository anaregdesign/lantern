package security

// LogPolicyVersion identifies the closed managed logging contract. Log format,
// listener configuration and secret values are outside this policy.
const LogPolicyVersion = 1

// LogLevel is independent of environment parsing and of any logging backend.
// Only these exact canonical spellings can enter committed policy.
type LogLevel string

const (
	LogLevelDebug LogLevel = "debug"
	LogLevelInfo  LogLevel = "info"
	LogLevelWarn  LogLevel = "warn"
	LogLevelError LogLevel = "error"
)

func (l LogLevel) valid() bool {
	switch l {
	case LogLevelDebug, LogLevelInfo, LogLevelWarn, LogLevelError:
		return true
	default:
		return false
	}
}

// LogPolicy is committed desired state. Absence means ownership has not been
// adopted. Generation is derived by management Apply, never from operator input.
type LogPolicy struct {
	Version    int      `json:"version"`
	Generation uint64   `json:"generation"`
	Level      LogLevel `json:"level"`
}

func (p *LogPolicy) valid() bool {
	return p == nil || p.Version == LogPolicyVersion && p.Generation != 0 && p.Level.valid()
}

func (p *LogPolicy) clone() *LogPolicy {
	if p == nil {
		return nil
	}
	copy := *p
	return &copy
}

func ValidateLogPolicyConfiguration(p *LogPolicy) error {
	if p == nil || !p.valid() {
		return ErrInvalidImage
	}
	return nil
}

// LogPolicyChange contains desired business meaning only. Writing a prior level
// is a new committed generation; there is no reset or return-to-env operation.
type LogPolicyChange struct {
	Version int      `json:"version"`
	Level   LogLevel `json:"level"`
}

func (p *LogPolicyChange) valid() bool {
	return p != nil && p.Version == LogPolicyVersion && p.Level.valid()
}

func ValidateLogPolicyChangeConfiguration(p *LogPolicyChange) error {
	if !p.valid() {
		return ErrInvalidImage
	}
	return nil
}
