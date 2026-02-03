package messaging

// Config holds RabbitMQ URL, exchange/queue names.
type Config struct {
	RabbitMQURL      string
	JobsExchange     string // cp.jobs
	ResultsExchange  string // cp.results
	ResultsQueue     string // control-plane.results
	ResultsBindKey   string // result.#
	ProgressQueue    string // control-plane.progress
	ProgressBindKey  string // progress.#
}

// DefaultConfig returns config with plan defaults.
func DefaultConfig(rabbitURL string) Config {
	return Config{
		RabbitMQURL:     rabbitURL,
		JobsExchange:    "cp.jobs",
		ResultsExchange: "cp.results",
		ResultsQueue:    "control-plane.results",
		ResultsBindKey:  "result.#",
		ProgressQueue:   "control-plane.progress",
		ProgressBindKey: "progress.#",
	}
}
