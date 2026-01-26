package workflow

// Valid transitions: CREATED -> QUEUED -> RUNNING -> COMPLETED | FAILED.
// QUEUED -> COMPLETED|FAILED allowed when workers send result without RUNNING event.
var validTransitions = map[string][]string{
	"CREATED":   {"QUEUED"},
	"QUEUED":    {"RUNNING", "COMPLETED", "FAILED"},
	"RUNNING":   {"COMPLETED", "FAILED"},
	"COMPLETED": {},
	"FAILED":    {},
}

// CanTransition returns true if from -> to is allowed.
func CanTransition(from, to string) bool {
	next, ok := validTransitions[from]
	if !ok {
		return false
	}
	for _, s := range next {
		if s == to {
			return true
		}
	}
	return false
}
