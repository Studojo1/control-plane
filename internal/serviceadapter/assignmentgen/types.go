package assignmentgen

// Payload assignment-gen job payload (subset of AssignmentSpec).
type Payload struct {
	AssignmentType   string  `json:"assignment_type"`
	Description      string  `json:"description"`
	LengthWords      *int    `json:"length_words,omitempty"`
	LengthPages      *float64 `json:"length_pages,omitempty"`
	FormatStyle      string  `json:"format_style"`
	CourseName       string  `json:"course_name,omitempty"`
	AllowWebSearch   bool    `json:"allow_web_search"`
	HumanizerConfig  *struct {
		Enabled   bool   `json:"enabled"`
		Intensity string `json:"intensity"`
	} `json:"humanizer_config,omitempty"`
}

// Result assignment-gen result (download_url from worker).
type Result struct {
	DocPath     string `json:"doc_path,omitempty"`
	DownloadURL string `json:"download_url,omitempty"`
}
