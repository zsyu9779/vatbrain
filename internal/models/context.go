package models

// SearchContext captures the current task context for Stage 1 (Contextual Gating)
// of the two-stage retrieval pipeline. It implements Design Principle 10
// (Encoding Specificity): memory retrieval is strongly dependent on the context
// in which the memory was encoded.
type SearchContext struct {
	ProjectID   string   `json:"project_id"`
	Language    string   `json:"language"`
	TaskType    TaskType `json:"task_type"`
	ActiveFiles []string `json:"active_files"`
	SessionID   string   `json:"session_id"`
}
